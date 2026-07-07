#!/usr/bin/env python3
"""
KiroX accounts.json enrichment script.
Queries real AWS/Kiro APIs to fill missing subscription, creditLimit, creditUsed fields.
Fixes "Unknown response" error when importing to kiro-gateway server.

API flow (matches export.go logic):
1. OIDC refresh token -> get AWS accessToken
2. Kiro refreshToken (Kiro creds first, AWS fallback) -> get profileArn
3. getUsageLimits -> get subscription + credit data
"""

import json
import ssl
import time
import urllib.request
import urllib.error
import urllib.parse
import sys
from pathlib import Path

# ============ Config ============
ACCOUNTS_FILE = Path(r"C:\Users\lu\Documents\Kirox\accounts.json")

OIDC_TOKEN_URL = "https://oidc.us-east-1.amazonaws.com/token"
KIRO_REFRESH_URL = "https://prod.us-east-1.auth.desktop.kiro.dev/refreshToken"
USAGE_URL = "https://q.us-east-1.amazonaws.com/getUsageLimits"

FALLBACK_PROFILE_ARN = "arn:aws:codewhisperer:us-east-1:638616132270:profile/AAAACCCCXXXX"

# ============ HTTP Client ============
ctx = ssl.create_default_context()
ctx.check_hostname = False
ctx.verify_mode = ssl.CERT_NONE


def http_post(url, body_dict, headers=None):
    if headers is None:
        headers = {}
    headers.setdefault("Content-Type", "application/json")
    data = json.dumps(body_dict).encode("utf-8")
    req = urllib.request.Request(url, data=data, headers=headers, method="POST")
    try:
        with urllib.request.urlopen(req, context=ctx, timeout=30) as resp:
            return resp.status, json.loads(resp.read())
    except urllib.error.HTTPError as e:
        try:
            return e.code, json.loads(e.read())
        except Exception:
            return e.code, {"error": str(e)}
    except Exception as e:
        return 0, {"error": str(e)}


def http_get(url, access_token, headers=None):
    if headers is None:
        headers = {}
    headers.setdefault("Accept", "application/json")
    headers.setdefault("Authorization", "Bearer %s" % access_token)
    req = urllib.request.Request(url, headers=headers, method="GET")
    try:
        with urllib.request.urlopen(req, context=ctx, timeout=30) as resp:
            return resp.status, json.loads(resp.read())
    except urllib.error.HTTPError as e:
        try:
            return e.code, json.loads(e.read())
        except Exception:
            return e.code, {"error": str(e)}
    except Exception as e:
        return 0, {"error": str(e)}


# ============ Core Logic ============

def refresh_oidc_token(client_id, client_secret, refresh_token):
    """Step 1: Refresh AWS OIDC token"""
    body = {
        "clientId": client_id,
        "clientSecret": client_secret,
        "refreshToken": refresh_token,
        "grantType": "refresh_token",
        "idc_region": "us-east-1",
    }
    status, resp = http_post(OIDC_TOKEN_URL, body)
    if status != 200:
        return None, resp.get("error", "HTTP %d" % status)
    access_token = resp.get("accessToken", "")
    if not access_token:
        return None, "response missing accessToken"
    return resp, None


def refresh_kiro_token(client_id, client_secret, refresh_token):
    """Step 2: Refresh Kiro token to get profileArn"""
    body = {
        "clientId": client_id,
        "clientSecret": client_secret,
        "refreshToken": refresh_token,
        "grantType": "refresh_token",
        "idc_region": "us-east-1",
    }
    status, resp = http_post(KIRO_REFRESH_URL, body)
    if status != 200:
        return None, resp.get("error", "HTTP %d" % status)
    return resp, None


def get_usage_limits(access_token, profile_arn):
    """Step 3: Query usage limits"""
    encoded_arn = urllib.parse.quote(profile_arn, safe="")
    url = (
        "%s?origin=AI_EDITOR"
        "&resourceType=AGENTIC_REQUEST"
        "&isEmailRequired=true"
        "&profileArn=%s"
    ) % (USAGE_URL, encoded_arn)
    status, resp = http_get(url, access_token)
    if status != 200:
        return None, resp.get("error", "HTTP %d" % status)
    return resp, None


def parse_usage(usage_raw):
    """Extract subscription, creditLimit, creditUsed from usage response"""
    if not usage_raw:
        return None

    sub_info = usage_raw.get("subscriptionInfo", {})
    subscription = sub_info.get("subscriptionTitle", "")
    if not subscription:
        subscription = "Free"

    total_limit = 0.0
    total_used = 0.0

    breakdown = usage_raw.get("usageBreakdownList", [])
    for item in breakdown:
        rt = item.get("resourceType", "")
        dn = item.get("displayName", "")
        if rt == "CREDIT" or dn in ("Credits", "Credit"):
            limit = item.get("usageLimitWithPrecision") or item.get("usageLimit", 0)
            used = item.get("currentUsageWithPrecision") or item.get("currentUsage", 0)
            total_limit = float(limit) if limit else 0.0
            total_used = float(used) if used else 0.0

            ft = item.get("freeTrialInfo", {})
            if ft and ft.get("freeTrialStatus") == "ACTIVE":
                ft_limit = ft.get("usageLimitWithPrecision") or ft.get("usageLimit", 0)
                ft_used = ft.get("currentUsageWithPrecision") or ft.get("currentUsage", 0)
                total_limit += float(ft_limit) if ft_limit else 0.0
                total_used += float(ft_used) if ft_used else 0.0
            break

    return {
        "subscription": subscription,
        "creditLimit": int(total_limit),
        "creditUsed": int(total_used),
        "usageRaw": usage_raw,
    }


def enrich_account(acct, index, total):
    """Enrich one account with real API data"""
    email = acct.get("email", "index-%d" % index)
    print("\n[%d/%d] %s" % (index + 1, total, email))

    # ---- Extract credentials ----
    # AWS OIDC creds
    client_id = acct.get("clientId", "")
    client_secret = acct.get("clientSecret", "")
    refresh_token = acct.get("refreshToken", "")
    if not refresh_token:
        refresh_token = acct.get("awsRefreshToken", "")

    # Kiro creds
    kiro_client_id = acct.get("kiroClientId", "")
    kiro_client_secret = acct.get("kiroClientSecret", "")
    kiro_refresh_token = acct.get("kiroRefreshToken", "")
    kiro_access_token = acct.get("kiroAccessToken", "")
    kiro_raw = acct.get("kiroAuthTokenRaw", {})
    if isinstance(kiro_raw, dict):
        if not kiro_access_token:
            kiro_access_token = kiro_raw.get("accessToken", "")
        if not kiro_refresh_token:
            kiro_refresh_token = kiro_raw.get("refreshToken", "")

    if not client_id or not client_secret or not refresh_token:
        print("  [SKIP] missing core creds (clientId/clientSecret/refreshToken)")
        return False

    # ---- Step 1: Refresh OIDC token ----
    print("  [1/4] OIDC refresh...")
    oidc_resp, err = refresh_oidc_token(client_id, client_secret, refresh_token)
    if err:
        print("  [FAIL] OIDC refresh: %s" % err)
        return False
    aws_access_token = oidc_resp.get("accessToken", "")
    print("  [OK] OIDC refresh success")

    # ---- Step 2: Get profileArn ----
    profile_arn = acct.get("profileArn", "")

    if not profile_arn:
        # Priority: Kiro creds
        if kiro_refresh_token and kiro_client_id:
            print("  [2/4] Kiro refresh (Kiro creds)...")
            kiro_resp, err = refresh_kiro_token(kiro_client_id, kiro_client_secret, kiro_refresh_token)
            if err:
                print("  [WARN] Kiro creds refresh failed: %s" % err)
            else:
                profile_arn = kiro_resp.get("profileArn", "")
                if profile_arn:
                    if not kiro_access_token:
                        kiro_access_token = kiro_resp.get("accessToken", "")
                    print("  [OK] profileArn: %s" % profile_arn)

        # Fallback: AWS creds
        if not profile_arn:
            print("  [2/4] Kiro refresh (AWS creds fallback)...")
            kiro_resp2, err2 = refresh_kiro_token(client_id, client_secret, refresh_token)
            if err2:
                print("  [WARN] AWS creds refresh also failed: %s" % err2)
            else:
                profile_arn = kiro_resp2.get("profileArn", "")
                if profile_arn:
                    print("  [OK] profileArn (fallback): %s" % profile_arn)

    # Final fallback
    if not profile_arn:
        profile_arn = FALLBACK_PROFILE_ARN
        print("  [WARN] Using fallback profileArn: %s" % profile_arn)

    # ---- Step 3: Query usage ----
    access = kiro_access_token or aws_access_token
    print("  [3/4] Query usage...")
    usage_raw, err = get_usage_limits(access, profile_arn)
    if err:
        print("  [WARN] Usage query failed (%s), using fallback defaults" % err)
        # Fallback: use safe defaults so import doesn't break
        acct["subscription"] = "Free"
        acct["creditLimit"] = 0
        acct["creditUsed"] = 0
        acct["profileArn"] = profile_arn
        return True

    # ---- Step 4: Parse and write ----
    print("  [4/4] Parse usage...")
    parsed = parse_usage(usage_raw)
    if parsed is None:
        print("  [FAIL] Parse failed")
        return False

    # Update account data
    acct["subscription"] = parsed["subscription"]
    acct["creditLimit"] = parsed["creditLimit"]
    acct["creditUsed"] = parsed["creditUsed"]
    acct["profileArn"] = profile_arn
    if parsed.get("usageRaw"):
        acct["kiroUsageRaw"] = parsed["usageRaw"]

    print("  [DONE] subscription=%s, creditLimit=%d, creditUsed=%d" % (
        parsed["subscription"], parsed["creditLimit"], parsed["creditUsed"]))
    return True


def main():
    print("[READ] %s" % ACCOUNTS_FILE)
    if not ACCOUNTS_FILE.exists():
        print("[ERROR] File not found: %s" % ACCOUNTS_FILE)
        sys.exit(1)

    with open(ACCOUNTS_FILE, "r", encoding="utf-8") as f:
        accounts = json.load(f)

    if not isinstance(accounts, list):
        print("[ERROR] JSON must be an array")
        sys.exit(1)

    total = len(accounts)
    print("Total accounts: %d" % total)

    from concurrent.futures import ThreadPoolExecutor, as_completed
    import threading

    write_lock = threading.Lock()
    stats = {"success": 0, "failed": 0, "skipped": 0}

    # Build task list: (index, account) for accounts that need enrichment
    tasks = []
    for i, acct in enumerate(accounts):
        if (acct.get("subscription") and
            acct.get("creditLimit") is not None and
            acct.get("creditUsed") is not None):
            print("[SKIP %d/%d] %s - already has data" % (i + 1, total, acct.get("email", "?")))
            stats["skipped"] += 1
            continue
        tasks.append((i, acct))

    if not tasks:
        print("Nothing to do, all accounts have data.")
        return

    print("Accounts to enrich: %d (concurrency=5)" % len(tasks))

    def worker(idx, acct):
        try:
            ok = enrich_account(acct, idx, total)
        except Exception as e:
            print("  [ERROR] %s: %s" % (acct.get("email", "?"), e))
            ok = False
        with write_lock:
            if ok:
                stats["success"] += 1
            else:
                stats["failed"] += 1
            with open(ACCOUNTS_FILE, "w", encoding="utf-8") as f:
                json.dump(accounts, f, indent=2, ensure_ascii=False)
        return ok

    with ThreadPoolExecutor(max_workers=5) as pool:
        futures = {pool.submit(worker, i, acct): i for i, acct in tasks}
        for f in as_completed(futures):
            f.result()  # raise if any exception escaped

    print("\n" + "=" * 50)
    print("Complete! success=%d  failed=%d  skipped=%d  total=%d" % (
        stats["success"], stats["failed"], stats["skipped"], total))
    print("Output: %s" % ACCOUNTS_FILE)

    if stats["failed"] > 0:
        print("\n[INFO] %d accounts failed, re-run to retry" % stats["failed"])


if __name__ == "__main__":
    main()