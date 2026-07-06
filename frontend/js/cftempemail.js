// ===== CF 临时邮箱 (Cloudflare Temp Email) 配置管理 =====

let cftempemailConfigs = [];
let cftempemailConfigStatus = {};

function _cftT(key, varsOrFallback, fallbackMaybe) {
  var vars = null, fallback = null;
  if (typeof varsOrFallback === 'string') {
    fallback = varsOrFallback;
  } else if (varsOrFallback && typeof varsOrFallback === 'object') {
    vars = varsOrFallback;
    if (typeof fallbackMaybe === 'string') fallback = fallbackMaybe;
  }
  if (window.I18N && typeof window.I18N.t === 'function') {
    var v = window.I18N.t(key, vars);
    if (v && v !== key) return v;
  }
  if (fallback != null) {
    if (vars) {
      return fallback.replace(/\{(\w+)\}/g, function(_, k) {
        return vars[k] != null ? vars[k] : '{' + k + '}';
      });
    }
    return fallback;
  }
  return key;
}

async function loadCFTempEmailConfigs() {
  try {
    const configs = await window.go.main.App.GetCFTempEmailConfigs();
    cftempemailConfigs = configs || [];
    loadCFTempEmailConfigStatus();
    updateCFTempEmailUI();
    return configs;
  } catch (e) {
    console.error('[CFTempEmail] 加载配置失败:', e);
    cftempemailConfigs = [];
    return [];
  }
}

function loadCFTempEmailConfigStatus() {
  try {
    const saved = localStorage.getItem('cftempemail-config-status');
    if (saved) {
      cftempemailConfigStatus = JSON.parse(saved);
    }
  } catch (e) {
    cftempemailConfigStatus = {};
  }
}

function saveCFTempEmailConfigStatus() {
  try {
    localStorage.setItem('cftempemail-config-status', JSON.stringify(cftempemailConfigStatus));
  } catch (e) {}
}

function updateCFTempEmailUI() {
  let activeCount = 0;
  cftempemailConfigs.forEach(cfg => {
    const status = cftempemailConfigStatus[cfg.name];
    if (status && status.tested && status.success) {
      activeCount++;
    }
  });

  const summaryEl = document.getElementById('settings-cftempemail-summary');
  if (summaryEl) {
    if (cftempemailConfigs.length === 0) {
      summaryEl.textContent = _cftT('cftempemail.summaryNone', '未配置');
    } else {
      summaryEl.textContent = _cftT('cftempemail.summaryActive', { n: cftempemailConfigs.length, m: activeCount }, '已配置 {n} 个，可用 {m} 个');
    }
  }

  renderCFTempEmailConfigList();
}

function parseDomainsTextCft(text) {
  if (!text) return [];
  return text.split(/[\s,;\n]+/).map(s => s.trim()).filter(Boolean);
}

function renderCFTempEmailConfigList() {
  const inlineList = document.getElementById('cftempemail-inline-list');
  if (!inlineList) return;

  if (cftempemailConfigs.length === 0) {
    inlineList.innerHTML = `
      <div class="moemail-empty-state">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <path d="M4 4h16c1.1 0 2 .9 2 2v12c0 1.1-.9 2-2 2H4c-1.1 0-2-.9-2-2V6c0-1.1.9-2 2-2z"></path>
          <polyline points="22,6 12,13 2,6"></polyline>
        </svg>
        <div>${_cftT('cftempemail.emptyInline', '暂无配置，请在上方添加 CF 临时邮箱配置')}</div>
      </div>
    `;
    return;
  }

  inlineList.innerHTML = cftempemailConfigs.map((cfg, idx) => {
    const status = cftempemailConfigStatus[cfg.name] || { tested: false };
    let dotClass = 'untested';
    let statusLabel = _cftT('status.untested', '未测试');
    let statusClass = 'untested';
    let domainsHtml = '';

    const domains = (status.domains && status.domains.length > 0) ? status.domains : (cfg.domains || []);
    if (domains.length > 0) {
      domainsHtml = '<div class="moemail-domain-tags">' +
        domains.map(d => '<span class="moemail-domain-tag">' + escapeHtml(d) + '</span>').join('') +
        '</div>';
    }

    if (status.tested && status.success) {
      dotClass = 'success';
      statusLabel = _cftT('status.available', '可用');
      statusClass = 'success';
    } else if (status.tested) {
      dotClass = 'error';
      statusLabel = _cftT('status.unavailable', '不可用');
      statusClass = 'error';
    }

    return `
      <div class="moemail-config-item">
        <div class="moemail-config-main">
          <div class="moemail-status-dot ${dotClass}"></div>
          <div class="moemail-config-info">
            <div class="moemail-config-name">${escapeHtml(cfg.name)}</div>
            <div class="moemail-config-details">
              <span class="moemail-config-url">${escapeHtml(cfg.url)}</span>
              <span class="moemail-config-status ${statusClass}">${statusLabel}</span>
            </div>
            ${domainsHtml}
          </div>
        </div>
        <div class="moemail-config-actions">
          <button onclick="testCFTempEmailConfigByIndex(${idx})" class="btn btn-secondary btn-sm">
            <svg viewBox="0 0 24 24" width="12" height="12" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <path d="M22 11.08V12a10 10 0 11-5.93-9.14"/>
              <polyline points="22 4 12 14.01 9 11.01"/>
            </svg>
            ${_cftT('common.test', '测试')}
          </button>
          <button onclick="deleteCFTempEmailConfig(${idx})" class="btn btn-secondary btn-sm" style="color:var(--danger);">
            <svg viewBox="0 0 24 24" width="12" height="12" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <polyline points="3 6 5 6 21 6"/>
              <path d="M19 6v14a2 2 0 01-2 2H7a2 2 0 01-2-2V6m3 0V4a2 2 0 012-2h4a2 2 0 012 2v2"/>
            </svg>
            ${_cftT('common.delete', '删除')}
          </button>
        </div>
      </div>
    `;
  }).join('');
}

function generateCFTempEmailName() {
  var prefix = _cftT('cftempemail.autoNamePrefix', '配置');
  let idx = cftempemailConfigs.length + 1;
  let name = prefix + ' ' + idx;
  while (cftempemailConfigs.some(c => c.name === name)) {
    idx++;
    name = prefix + ' ' + idx;
  }
  return name;
}

async function inlineAddCFTempEmail() {
  var name = (document.getElementById('cftempemail-inline-name').value || '').trim();
  var url = (document.getElementById('cftempemail-inline-url').value || '').trim();
  var adminAuth = (document.getElementById('cftempemail-inline-adminauth').value || '').trim();
  var domainsText = (document.getElementById('cftempemail-inline-domains').value || '').trim();

  if (!url || !adminAuth) {
    showToast(_cftT('cftempemail.requiredFields', '请填写 Worker URL 和 Admin 鉴权密码'), 'error');
    return;
  }
  if (!name) name = generateCFTempEmailName();
  if (cftempemailConfigs.some(c => c.name === name)) {
    showToast(_cftT('cftempemail.nameExists', '配置名称已存在'), 'error');
    return;
  }

  const domains = parseDomainsTextCft(domainsText);

  // 先测试连接
  var btn = document.getElementById('cftempemail-inline-test-btn');
  var statusEl = document.getElementById('cftempemail-inline-status');
  var btnOriginalHTML = btn ? btn.innerHTML : '';
  if (btn) { btn.disabled = true; btn.textContent = _cftT('cftempemail.testing', '测试中...'); }
  if (statusEl) { statusEl.style.color = ''; statusEl.textContent = ''; }

  var testPayload = { name: name, url: url, adminAuth: adminAuth, domains: domains };
  var testResult;
  try {
    testResult = await window.go.main.App.TestCFTempEmailConnection(JSON.stringify(testPayload));
  } catch (e) {
    testResult = { error: String(e) };
  } finally {
    if (btn) { btn.disabled = false; btn.innerHTML = btnOriginalHTML; }
  }

  if (!testResult || testResult.error) {
    var errMsg = (testResult && testResult.error) || _cftT('cftempemail.testFailedShort', '测试失败');
    if (statusEl) { statusEl.style.color = 'var(--danger)'; statusEl.textContent = errMsg; }
    showToast(_cftT('cftempemail.cannotSaveUntilOk', '连接测试未通过，未保存配置：') + errMsg, 'error');
    return;
  }

  const newConfig = { name: name, url: url, adminAuth: adminAuth, domains: domains };
  cftempemailConfigs.push(newConfig);
  const saveResult = await window.go.main.App.SaveCFTempEmailConfigs(JSON.stringify(cftempemailConfigs));
  if (saveResult.error) {
    cftempemailConfigs.pop();
    showToast(_cftT('toast.operationFailed', '保存失败') + ': ' + saveResult.error, 'error');
    return;
  }

  cftempemailConfigStatus[name] = { tested: true, success: true, domains: domains };
  saveCFTempEmailConfigStatus();

  document.getElementById('cftempemail-inline-name').value = '';
  document.getElementById('cftempemail-inline-url').value = '';
  document.getElementById('cftempemail-inline-adminauth').value = '';
  document.getElementById('cftempemail-inline-domains').value = '';
  if (statusEl) { statusEl.style.color = 'var(--success)'; statusEl.textContent = ''; }

  if (domains.length > 0) {
    showToast(_cftT('cftempemail.addedWithDomains', { name: name, n: domains.length }, '已添加 {name}，{n} 个域名'), 'success');
  } else {
    showToast(_cftT('cftempemail.addedNamed', { name: name }, '已添加: {name}'));
  }
  renderCFTempEmailConfigList();
  updateCFTempEmailUI();
}

async function inlineTestCFTempEmail() {
  var url = (document.getElementById('cftempemail-inline-url').value || '').trim();
  var adminAuth = (document.getElementById('cftempemail-inline-adminauth').value || '').trim();
  var domainsText = (document.getElementById('cftempemail-inline-domains').value || '').trim();

  if (!url || !adminAuth) {
    showToast(_cftT('cftempemail.requiredFields', '请填写 Worker URL 和 Admin 鉴权密码'), 'error');
    return;
  }
  var btn = document.getElementById('cftempemail-inline-test-btn');
  var statusEl = document.getElementById('cftempemail-inline-status');
  var btnOriginalHTML = btn ? btn.innerHTML : '';
  btn.disabled = true; btn.textContent = _cftT('cftempemail.testing', '测试中...');
  if (statusEl) statusEl.textContent = '';
  try {
    var result = await window.go.main.App.TestCFTempEmailConnection(JSON.stringify({
      name: 'inline-test', url: url, adminAuth: adminAuth, domains: parseDomainsTextCft(domainsText)
    }));
    if (result.success) {
      if (statusEl) {
        statusEl.style.color = 'var(--success)';
        statusEl.textContent = _cftT('cftempemail.connectedOk', '连接成功');
      }
    } else {
      if (statusEl) {
        statusEl.style.color = 'var(--danger)';
        statusEl.textContent = result.error || _cftT('cftempemail.testFailed', '连接失败');
      }
    }
  } catch(e) {
    if (statusEl) {
      statusEl.style.color = 'var(--danger)';
      statusEl.textContent = _cftT('cftempemail.testFailedShort', '测试失败');
    }
  }
  btn.disabled = false; btn.innerHTML = btnOriginalHTML;
}

async function testCFTempEmailConfigByIndex(index) {
  if (index < 0 || index >= cftempemailConfigs.length) return;
  const config = cftempemailConfigs[index];
  try {
    const result = await window.go.main.App.TestCFTempEmailConnection(JSON.stringify(config));
    if (result.error) {
      cftempemailConfigStatus[config.name] = { tested: true, success: false };
      saveCFTempEmailConfigStatus();
      renderCFTempEmailConfigList();
      updateCFTempEmailUI();
      showToast(config.name + ': ' + result.error, 'error');
    } else {
      const domains = result.domains || [];
      cftempemailConfigStatus[config.name] = { tested: true, success: true, domains: domains };
      saveCFTempEmailConfigStatus();
      renderCFTempEmailConfigList();
      updateCFTempEmailUI();
      if (domains.length > 0) {
        showToast(config.name + ': ' + _cftT('cftempemail.testOkWithDomains', { n: domains.length }, '连接成功，{n} 个域名'), 'success');
      } else {
        showToast(config.name + ': ' + _cftT('cftempemail.testOkNoDomain', '连接成功'), 'success');
      }
    }
  } catch (e) {
    cftempemailConfigStatus[config.name] = { tested: true, success: false };
    saveCFTempEmailConfigStatus();
    renderCFTempEmailConfigList();
    updateCFTempEmailUI();
    showToast(config.name + ': ' + _cftT('cftempemail.testFailedShort', '测试失败'), 'error');
  }
}

async function deleteCFTempEmailConfig(index) {
  if (index < 0 || index >= cftempemailConfigs.length) return;
  const configName = cftempemailConfigs[index].name;
  showConfirmModal(
    _cftT('cftempemail.deleteConfigTitle', '删除配置'),
    _cftT('cftempemail.deleteConfigMsg', { name: configName }, '确认删除配置 "{name}" 吗？'),
    _cftT('accounts.deleteConfirm', '确认删除'),
    async function() {
      cftempemailConfigs.splice(index, 1);
      try {
        const result = await window.go.main.App.SaveCFTempEmailConfigs(JSON.stringify(cftempemailConfigs));
        if (result.error) {
          showToast(_cftT('toast.deleteFailed', '删除失败') + ': ' + result.error, 'error');
          await loadCFTempEmailConfigs();
          return;
        }
        delete cftempemailConfigStatus[configName];
        saveCFTempEmailConfigStatus();
        updateCFTempEmailUI();
        showToast(_cftT('toast.deleteOk', '删除成功'), 'success');
      } catch (e) {
        showToast(_cftT('toast.deleteFailed', '删除失败') + ': ' + e, 'error');
        await loadCFTempEmailConfigs();
      }
    }
  );
}

async function clearAllCFTempEmailConfigs() {
  if (cftempemailConfigs.length === 0) {
    showToast(_cftT('cftempemail.nothingToClear', '没有配置可清空'), 'info');
    return;
  }
  showConfirmModal(
    _cftT('cftempemail.clearAllTitle', '清空 CF 临时邮箱配置'),
    _cftT('cftempemail.clearAllMsg', '确认清空所有 CF 临时邮箱配置吗？此操作不可恢复。'),
    _cftT('accounts.clearAllConfirm', '确认清空'),
    async function() {
      cftempemailConfigs = [];
      try {
        const result = await window.go.main.App.SaveCFTempEmailConfigs(JSON.stringify(cftempemailConfigs));
        if (result.error) {
          showToast(_cftT('toast.clearFailed', '清空失败') + ': ' + result.error, 'error');
          await loadCFTempEmailConfigs();
          return;
        }
        cftempemailConfigStatus = {};
        saveCFTempEmailConfigStatus();
        updateCFTempEmailUI();
        showToast(_cftT('cftempemail.allCleared', '已清空所有配置'), 'success');
      } catch (e) {
        showToast(_cftT('toast.clearFailed', '清空失败') + ': ' + e, 'error');
        await loadCFTempEmailConfigs();
      }
    }
  );
}

async function autoTestAllCFTempEmailConfigs() {
  if (cftempemailConfigs.length === 0) return;
  console.log('[CFTempEmail] 启动自动测试，共 ' + cftempemailConfigs.length + ' 个配置');
  for (let i = 0; i < cftempemailConfigs.length; i++) {
    const config = cftempemailConfigs[i];
    try {
      const result = await window.go.main.App.TestCFTempEmailConnection(JSON.stringify(config));
      if (result.error) {
        cftempemailConfigStatus[config.name] = { tested: true, success: false };
      } else {
        const domains = result.domains || [];
        cftempemailConfigStatus[config.name] = { tested: true, success: true, domains: domains };
      }
    } catch (e) {
      cftempemailConfigStatus[config.name] = { tested: true, success: false };
    }
  }
  saveCFTempEmailConfigStatus();
  updateCFTempEmailUI();
  console.log('[CFTempEmail] 自动测试完成');
}

document.addEventListener('DOMContentLoaded', async function() {
  await loadCFTempEmailConfigs();
  autoTestAllCFTempEmailConfigs();
});

window.addEventListener('i18n:changed', function() {
  try { if (typeof updateCFTempEmailUI === 'function') updateCFTempEmailUI(); } catch (e) {}
});