// ===== Outlook 账号管理 =====

var outlookCurrentPage = 1;
var outlookPageSize = 10;
var outlookAllAccounts = [];
var outlookSelectedEmails = {};

function _accT(key, varsOrFallback, fallbackMaybe) {
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

function escapeAccountHtml(s) {
  if (s == null) return '';
  return String(s).replace(/[&<>"']/g, function(c) {
    return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
  });
}

function quoteAccountArg(s) {
  return String(s || '').replace(/\\/g, '\\\\').replace(/'/g, "\\'");
}

function openAddOutlookModal() {
  document.getElementById('add-outlook-modal').classList.add('show');
}

function closeAddOutlookModal() {
  document.getElementById('add-outlook-modal').classList.remove('show');
  document.getElementById('cfg-outlook-data').value = '';
}

async function addOutlookAccounts() {
  var data = document.getElementById('cfg-outlook-data').value.trim();
  if (!data) {
    showToast(_accT('accounts.inputRequired', '请先输入 Outlook 账号数据'), 'error');
    return;
  }
  try {
    var result = await window.go.main.App.AddOutlookAccounts(data);
    if (result.error) {
      showToast(result.error, 'error');
      return;
    }

    closeAddOutlookModal();
    await loadOutlookAccountsList();
    showToast(_accT('accounts.addedSummary', { n: result.added, total: result.total }, '成功添加 {n} 个账号，当前共 {total} 个'));
  } catch(e) {
    showToast(_accT('toast.addFailed', '添加失败') + ': ' + e.message, 'error');
  }
}

async function importOutlookFile() {
  try {
    var filePath = await window.go.main.App.SelectOutlookFile();
    if (!filePath) {
      return;
    }

    var result = await window.go.main.App.ImportOutlookFile(filePath);
    if (result.error) {
      showToast(result.error, 'error');
      return;
    }

    await loadOutlookAccountsList();
    closeAddOutlookModal();
    showToast(_accT('accounts.importSummary', { n: result.added, total: result.total }, '成功导入 {n} 个账号，当前共 {total} 个'));
  } catch(e) {
    showToast(_accT('accounts.importFailed', '导入失败') + ': ' + e.message, 'error');
  }
}

async function loadOutlookAccountsList() {
  try {
    var accounts = await window.go.main.App.GetOutlookAccounts();
    outlookAllAccounts = accounts || [];
    var existing = {};
    outlookAllAccounts.forEach(function(a) {
      if (a && a.email) existing[a.email] = true;
    });
    Object.keys(outlookSelectedEmails).forEach(function(email) {
      if (!existing[email]) delete outlookSelectedEmails[email];
    });
    renderOutlookPage();
  } catch(e) {
    console.error('加载账号列表失败:', e);
  }
}

function renderOutlookPage() {
  var accounts = outlookAllAccounts;
  var tbody = document.getElementById('parsed-outlook-body');
  var pager = document.getElementById('outlook-pager');
  var countEl = document.getElementById('outlook-count');

  if (countEl) countEl.textContent = accounts ? accounts.length : 0;

  if (accounts && accounts.length > 0) {
    var total = accounts.length;
    var totalPages = Math.ceil(total / outlookPageSize);
    if (outlookCurrentPage > totalPages) outlookCurrentPage = totalPages;
    if (outlookCurrentPage < 1) outlookCurrentPage = 1;

    var start = (outlookCurrentPage - 1) * outlookPageSize;
    var end = Math.min(start + outlookPageSize, total);
    var pageAccounts = accounts.slice(start, end);

    var html = '';
    pageAccounts.forEach(function(acc, i) {
      var globalIdx = start + i;
      var email = acc.email || '';
      var status = acc.registered
        ? (acc.success ? _accT('status.success', '成功') : _accT('status.failed', '失败'))
        : _accT('status.unregistered', '未注册');
      var statusColor = acc.registered ? (acc.success ? 'var(--success)' : 'var(--danger)') : 'var(--text-muted)';
      var addedTime = acc.addedAt ? acc.addedAt.substring(5, 16) : '-';
      html += '<tr><td style="padding:10px 16px;"><input type="checkbox" data-outlook-email="' + escapeAccountHtml(email) + '" ' + (outlookSelectedEmails[email] ? 'checked' : '') + ' onchange="toggleOutlookRowSelection(\'' + quoteAccountArg(email) + '\', this.checked)"></td>';
      html += '<td>' + (globalIdx+1) + '</td><td>' + escapeAccountHtml(email) + '</td>';
      html += '<td style="color:' + statusColor + ';font-weight:600;">' + status + '</td>';
      html += '<td style="font-size:11px;color:var(--text-muted);font-family:var(--font-mono);">' + addedTime + '</td>';
      html += '<td style="text-align:right;"><a href="javascript:void(0)" onclick="deleteOutlookAccount(\'' + quoteAccountArg(email) + '\')" style="color:var(--danger);">' + _accT('common.delete', '删除') + '</a></td></tr>';
    });
    tbody.innerHTML = html;

    if (totalPages > 1) {
      pager.style.display = 'flex';
      document.getElementById('outlook-pager-info').textContent = _accT('accounts.pagerInfo', { cur: outlookCurrentPage, total: totalPages, n: total }, '第 {cur} / {total} 页 (共 {n} 个)');
      document.getElementById('outlook-pager-prev').disabled = outlookCurrentPage <= 1;
      document.getElementById('outlook-pager-next').disabled = outlookCurrentPage >= totalPages;
    } else {
      pager.style.display = 'none';
    }
  } else {
    tbody.innerHTML = '<tr><td colspan="6" style="text-align:center;color:var(--text-muted);padding:20px;">' + _accT('accounts.emptyRow', '暂无邮箱账号') + '</td></tr>';
    pager.style.display = 'none';
  }
  refreshOutlookSelectionControls();
}

function changeOutlookPage(delta) {
  outlookCurrentPage += delta;
  if (outlookCurrentPage < 1) outlookCurrentPage = 1;
  renderOutlookPage();
}

async function deleteOutlookAccount(email) {
  showConfirmModal(
    _accT('accounts.deleteTitle', '删除账号'),
    _accT('accounts.deleteMsg', { email: email }, '确认删除账号 {email} ?'),
    _accT('accounts.deleteConfirm', '确认删除'),
    async function() {
      try {
        var result = await window.go.main.App.DeleteOutlookAccount(email);
        if (result.error) {
          showToast(result.error, 'error');
          return;
        }
        delete outlookSelectedEmails[email];
        showToast(_accT('accounts.deletedOne', '账号已删除'));
        await loadOutlookAccountsList();
      } catch(e) {
        showToast(_accT('toast.deleteFailed', '删除失败') + ': ' + e.message, 'error');
      }
    }
  );
}

function getCurrentOutlookPageAccounts() {
  var accounts = outlookAllAccounts || [];
  var totalPages = Math.ceil(accounts.length / outlookPageSize);
  if (totalPages > 0 && outlookCurrentPage > totalPages) outlookCurrentPage = totalPages;
  if (outlookCurrentPage < 1) outlookCurrentPage = 1;
  var start = (outlookCurrentPage - 1) * outlookPageSize;
  var end = Math.min(start + outlookPageSize, accounts.length);
  return accounts.slice(start, end);
}

function getSelectedOutlookEmails() {
  return Object.keys(outlookSelectedEmails).filter(function(email) { return outlookSelectedEmails[email]; });
}

function refreshOutlookSelectionControls() {
  var selected = getSelectedOutlookEmails();
  var btn = document.getElementById('outlook-delete-selected');
  if (btn) {
    btn.disabled = selected.length === 0;
    btn.textContent = selected.length
      ? _accT('accounts.deleteSelectedCount', { n: selected.length }, '批量删除 ({n})')
      : _accT('accounts.deleteSelected', '批量删除');
  }

  var pageChk = document.getElementById('outlook-select-page');
  if (!pageChk) return;
  var pageAccounts = getCurrentOutlookPageAccounts().filter(function(a) { return a && a.email; });
  var checked = pageAccounts.filter(function(a) { return outlookSelectedEmails[a.email]; }).length;
  pageChk.checked = pageAccounts.length > 0 && checked === pageAccounts.length;
  pageChk.indeterminate = checked > 0 && checked < pageAccounts.length;
  pageChk.disabled = pageAccounts.length === 0;
}

function toggleOutlookRowSelection(email, checked) {
  if (!email) return;
  if (checked) outlookSelectedEmails[email] = true;
  else delete outlookSelectedEmails[email];
  refreshOutlookSelectionControls();
}

function toggleOutlookPageSelection(checked) {
  getCurrentOutlookPageAccounts().forEach(function(a) {
    if (!a || !a.email) return;
    if (checked) outlookSelectedEmails[a.email] = true;
    else delete outlookSelectedEmails[a.email];
  });
  renderOutlookPage();
}

function deleteSelectedOutlookAccounts() {
  var selected = getSelectedOutlookEmails();
  if (!selected.length) {
    showToast(_accT('accounts.noSelected', '请先勾选要删除的账号'), 'error');
    return;
  }
  showConfirmModal(
    _accT('accounts.deleteSelectedTitle', '批量删除账号'),
    _accT('accounts.deleteSelectedMsg', { n: selected.length }, '确认删除选中的 {n} 个账号？此操作不可恢复。'),
    _accT('accounts.deleteConfirm', '确认删除'),
    async function() {
      try {
        var result = await window.go.main.App.DeleteOutlookAccounts(JSON.stringify(selected));
        if (result.error) {
          showToast(result.error, 'error');
          return;
        }
        outlookSelectedEmails = {};
        showToast(_accT('toast.accountsDeleted', { n: (result.removed || 0) }, '已删除 {n} 个账号'));
        await loadOutlookAccountsList();
      } catch(e) {
        showToast(_accT('toast.deleteFailed', '删除失败') + ': ' + e.message, 'error');
      }
    }
  );
}

function clearAllOutlookAccounts() {
  showConfirmModal(
    _accT('accounts.clearAllTitle', '清空微软邮箱'),
    _accT('accounts.clearAllMsg', '确认清空所有微软邮箱账号？此操作不可恢复！'),
    _accT('accounts.clearAllConfirm', '确认清空'),
    async function() {
      try {
        var result = await window.go.main.App.ClearOutlookAccounts();
        if (result.error) {
          showToast(result.error, 'error');
          return;
        }
        outlookSelectedEmails = {};
        showToast(_accT('accounts.allCleared', '已清空所有账号'));
        await loadOutlookAccountsList();
      } catch(e) {
        showToast(_accT('toast.clearFailed', '清空失败') + ': ' + e.message, 'error');
      }
    }
  );
}

function clearRegisteredOutlookAccounts() {
  var registered = outlookAllAccounts.filter(function(a) { return a.registered; }).length;
  if (!registered) {
    showToast(_accT('accounts.noRegistered', '没有已注册的账号'));
    return;
  }
  showConfirmModal(
    _accT('accounts.clearRegisteredTitle', '清除已注册'),
    _accT('accounts.clearRegisteredMsg', { n: registered }, '确认删除 {n} 个已注册（成功/失败）的账号？'),
    _accT('accounts.deleteConfirm', '确认删除'),
    async function() {
      try {
        var result = await window.go.main.App.ClearRegisteredOutlookAccounts();
        if (result.error) {
          showToast(result.error, 'error');
          return;
        }
        outlookSelectedEmails = {};
        showToast(_accT('toast.accountsDeleted', { n: (result.removed || 0) }, '已删除 {n} 个账号'));
        await loadOutlookAccountsList();
      } catch(e) {
        showToast(_accT('toast.deleteFailed', '删除失败') + ': ' + e.message, 'error');
      }
    }
  );
}

function openOutlookModal() {
  switchPage('accounts');
  loadOutlookAccountsList();
}

// ===== 自动刷新（停留在邮箱池页时每 3 秒刷新状态） =====
var outlookRefreshTimer = null;

function startOutlookAutoRefresh() {
  stopOutlookAutoRefresh();
  outlookRefreshTimer = setInterval(loadOutlookAccountsList, 3000);
}

function stopOutlookAutoRefresh() {
  if (outlookRefreshTimer) {
    clearInterval(outlookRefreshTimer);
    outlookRefreshTimer = null;
  }
}

// 语言切换后重新渲染表格行（状态/操作链接等动态文本）
window.addEventListener('i18n:changed', function() {
  try { if (typeof renderOutlookPage === 'function') renderOutlookPage(); } catch (e) {}
});
