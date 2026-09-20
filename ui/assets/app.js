(() => {
  'use strict';
  const token = new URLSearchParams(location.hash.slice(1)).get('bridge_token') || '';
  const pending = new Map();
  let sequence = 0;
  let latestAccounts = [];

  function request(type, payload = {}, timeout = 15000) {
    const requestId = `${Date.now().toString(36)}-${(++sequence).toString(36)}`;
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => { pending.delete(requestId); reject(new Error('操作超时')); }, timeout);
      pending.set(requestId, { resolve, reject, timer });
      parent.postMessage({ source: 'sub2api-plugin-ui', bridge_token: token, type, request_id: requestId, ...payload }, '*');
    });
  }

  function notify(level, message) {
    parent.postMessage({ source: 'sub2api-plugin-ui', bridge_token: token, type: 'ui.notify', request_id: `n-${++sequence}`, level, message }, '*');
  }

  window.addEventListener('message', (event) => {
    const message = event.data;
    if (event.source !== parent || !message || message.source !== 'sub2api-plugin-host' || message.bridge_token !== token) return;
    const task = pending.get(message.request_id);
    if (!task) return;
    clearTimeout(task.timer); pending.delete(message.request_id);
    if (message.ok) task.resolve(message); else task.reject(new Error(message.error || '操作失败'));
  });

  const $ = (id) => document.getElementById(id);
  const escapeHTML = (value) => String(value ?? '').replace(/[&<>'"]/g, (char) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', "'": '&#39;', '"': '&quot;' })[char]);
  const formatTime = (value) => {
    if (!value) return '未知';
    const date = new Date(value);
    return Number.isNaN(date.getTime()) ? '未知' : new Intl.DateTimeFormat('zh-CN', { dateStyle: 'medium', timeStyle: 'short' }).format(date);
  };

  function renderStatus(status) {
    $('total').textContent = status.total ?? 0;
    $('paid').textContent = status.paid ?? 0;
    $('expiring').textContent = status.expiring_soon ?? 0;
    $('failed').textContent = status.failed ?? 0;
    $('subtitle').textContent = status.message || '监控运行中';
    $('updated').textContent = status.last_refresh_at ? `更新于 ${formatTime(status.last_refresh_at)}` : '等待首次刷新';
    const accounts = Array.isArray(status.accounts) ? status.accounts : [];
    latestAccounts = accounts;
    $('accounts').innerHTML = accounts.length ? accounts.map(renderAccount).join('') : '<div class="empty">没有可监控的 OpenAI OAuth 账号</div>';
    resize();
  }

  function renderAccount(account) {
    const error = account.error || '';
    const plan = error ? '检查失败' : (account.plan_type || 'unknown');
    const paid = !['free', 'unknown'].includes(String(account.plan_type || '').toLowerCase());
    const primary = renderUsageSummary(account.usage?.primary_window, '5 小时');
    const secondary = renderUsageSummary(account.usage?.secondary_window, '7 天');
    return `<article class="account">
      <div class="identity"><div class="identity-heading"><strong>${escapeHTML(account.email || `站内账号 #${account.host_account_id}`)}</strong><button class="stats-button" type="button" data-account-id="${Number(account.host_account_id)}" aria-label="查看官方统计" title="查看官方统计"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M3 3v18h18M7 16l4-5 4 3 5-7"/></svg></button></div><small>${escapeHTML(account.account_id || `ID ${account.host_account_id}`)}</small></div>
      <span class="badge ${error ? 'error' : paid ? 'paid' : ''}">${escapeHTML(plan)}</span>
      <div class="expiry"><small>${account.will_renew === true ? '自动续费' : account.will_renew === false ? '不自动续费' : '续费状态未知'}</small><strong>${error ? escapeHTML(error) : formatTime(account.active_until)}</strong></div>
      <div class="usage-summary">${primary}${secondary}</div>
    </article>`;
  }

  function renderUsageSummary(window, fallbackLabel) {
    if (!window) return `<div><small>${fallbackLabel}</small><strong>暂无数据</strong></div>`;
    const label = usageWindowLabel(window, fallbackLabel);
    const used = normalizedPercent(window.used_percent);
    return `<div><small>${label}</small><strong>${used.toFixed(1)}%</strong><div class="bar"><span class="${usageTone(used)}" style="width:${used}%"></span></div></div>`;
  }

  function renderUsageWindow(window, fallbackLabel) {
    if (!window) return `<div class="usage-window"><small>${fallbackLabel}用量</small><strong>暂无数据</strong></div>`;
    const label = usageWindowLabel(window, fallbackLabel);
    const used = normalizedPercent(window.used_percent);
    const resetAt = Number(window.reset_at || 0) * 1000 || (Date.now() + Number(window.reset_after_seconds || 0) * 1000);
    return `<div class="usage-window"><small>${label}用量</small><strong>${used.toFixed(1)}%</strong><span>${formatTime(resetAt)} 重置</span><div class="bar"><span class="${usageTone(used)}" style="width:${used}%"></span></div></div>`;
  }

  function usageWindowLabel(window, fallbackLabel) {
    const seconds = Number(window?.limit_window_seconds || 0);
    return seconds >= 6 * 86400 ? '7 天' : seconds > 0 && seconds <= 6 * 3600 ? '5 小时' : fallbackLabel;
  }

  const normalizedPercent = (value) => Math.max(0, Math.min(100, Number(value || 0)));
  const usageTone = (used) => used >= 90 ? 'danger' : used >= 70 ? 'warn' : '';

  function renderCredits(credits) {
    if (!credits) return '<div class="credit-status"><small>账号积分 / 额度</small><strong>官方未提供</strong></div>';
    const balance = credits.unlimited ? '无限' : credits.balance != null ? String(credits.balance) : credits.has_credits ? '可用' : '无';
    const local = Array.isArray(credits.approx_local_messages) && credits.approx_local_messages.length ? `本地约 ${credits.approx_local_messages.join('–')} 条` : '';
    const cloud = Array.isArray(credits.approx_cloud_messages) && credits.approx_cloud_messages.length ? `云端约 ${credits.approx_cloud_messages.join('–')} 条` : '';
    const detail = credits.overage_limit_reached ? '超额额度已达上限' : [local, cloud].filter(Boolean).join(' · ') || '官方积分余额';
    return `<div class="credit-status"><small>账号积分 / 额度</small><strong>${escapeHTML(balance)}</strong><span>${escapeHTML(detail)}</span></div>`;
  }

  function renderResetCredits(resetCredits) {
    if (!resetCredits) return '<div class="reset-credits"><small>重置卡</small><strong>官方未提供</strong></div>';
    const credits = Array.isArray(resetCredits.credits) ? resetCredits.credits.filter((credit) => credit?.expires_at) : [];
    const expirations = credits.length ? `<span class="reset-expirations">${credits.map((credit, index) => `第 ${index + 1} 张：${escapeHTML(formatTime(credit.expires_at))}`).join('<br>')}</span>` : '<span>暂无到期明细</span>';
    const applicable = Number(resetCredits.applicable_available_count || 0);
    return `<div class="reset-credits"><small>重置卡</small><strong>${Number(resetCredits.available_count || 0)} 张${applicable > 0 ? ` · 当前可用 ${applicable} 张` : ''}</strong>${expirations}</div>`;
  }

  function renderOfficialUsage(official) {
    const days = Array.isArray(official?.days) ? official.days : [];
    if (!days.length) return '<section class="detail-section"><h3>官方日统计</h3><div class="detail-empty">暂无官方日统计数据</div></section>';
    const maxCredits = Math.max(...days.map((day) => Number(day.totals?.credits || 0)), 0);
    const rows = days.map((day) => {
      const credits = Number(day.totals?.credits || 0);
      const width = maxCredits > 0 ? Math.max(2, credits / maxCredits * 100) : 0;
      return `<div class="daily-row"><span>${escapeHTML(String(day.date || '').slice(5))}</span><div class="daily-track"><i style="width:${width}%"></i></div><strong>${credits.toFixed(2)}</strong><small>${formatCompact(day.totals?.text_total_tokens)} tokens · ${Number(day.totals?.turns || 0)} 轮</small></div>`;
    }).join('');
    const clients = aggregateUsage(days, 'clients', 'client_id').slice(0, 6);
    const clientRows = clients.length ? clients.map((item) => `<div class="breakdown-row"><span>${escapeHTML(item.label)}</span><strong>${item.credits.toFixed(2)} credits · ${item.turns} 轮</strong></div>`).join('') : '<div class="detail-empty">暂无客户端拆分</div>';
    const models = aggregateUsage(days, 'models', 'model').sort((a, b) => b.turns - a.turns).slice(0, 6);
    const modelRows = models.length ? models.map((item) => `<div class="breakdown-row"><span>${escapeHTML(item.label)}</span><strong>${item.turns} 轮</strong></div>`).join('') : '<div class="detail-empty">暂无模型拆分</div>';
    const rate = Number(official.credits_per_usd || 25);
    const usd = Number(official.total_credits || 0) / rate;
    return `<section class="detail-section"><div class="section-heading"><h3>官方日统计</h3><span>最近 7 天</span></div><div class="official-totals"><div><small>已用 Credits</small><strong>${Number(official.total_credits || 0).toFixed(2)}</strong></div><div><small>折算成本</small><strong>$${usd.toFixed(2)}</strong></div><div><small>Tokens</small><strong>${formatCompact(official.total_tokens)}</strong></div><div><small>轮次</small><strong>${Number(official.total_turns || 0)}</strong></div></div><div class="daily-chart">${rows}</div><h4>客户端拆分</h4><div class="breakdown-list">${clientRows}</div><h4>模型轮次</h4><div class="breakdown-list">${modelRows}</div></section>`;
  }

  function aggregateUsage(days, field, labelKey) {
    const totals = new Map();
    for (const day of days) {
      for (const item of Array.isArray(day?.[field]) ? day[field] : []) {
        const label = String(item?.[labelKey] || '未知');
        const current = totals.get(label) || { label, credits: 0, turns: 0 };
        current.credits += Number(item.credits || 0);
        current.turns += Number(item.turns || 0);
        totals.set(label, current);
      }
    }
    return [...totals.values()].sort((a, b) => b.credits - a.credits || b.turns - a.turns);
  }

  function formatCompact(value) {
    const number = Number(value || 0);
    return new Intl.NumberFormat('zh-CN', { notation: number >= 10000 ? 'compact' : 'standard', maximumFractionDigits: 1 }).format(number);
  }

  function openAccountDetail(account) {
    $('detail-account').textContent = `${account.email || `站内账号 #${account.host_account_id}`} · ${account.plan_type || 'unknown'}`;
    $('detail-body').innerHTML = `<section class="detail-section"><h3>额度概览</h3><div class="detail-metrics">${renderUsageWindow(account.usage?.primary_window, '5 小时')}${renderUsageWindow(account.usage?.secondary_window, '7 天')}${renderCredits(account.credits)}${renderResetCredits(account.reset_credits)}</div></section>${renderOfficialUsage(account.official_usage)}`;
    $('account-detail').showModal();
  }

  async function loadConfig() {
    const response = await request('config.load');
    const config = response.config || {};
    $('interval').value = config.interval_minutes ?? 30;
    $('timeout').value = config.request_timeout_seconds ?? 20;
    $('concurrency').value = config.max_concurrency ?? 3;
    $('usage').checked = config.include_usage !== false;
    $('mask-identity').checked = config.mask_account_identity !== false;
  }

  async function loadStatus() {
    try {
      const response = await request('plugin.status');
      const raw = response.result?.status_json;
      renderStatus(raw ? JSON.parse(raw) : { message: response.result?.message || '插件未运行', accounts: [] });
    } catch (error) {
      $('subtitle').textContent = error.message;
    }
  }

  function resize() {
    parent.postMessage({ source: 'sub2api-plugin-ui', bridge_token: token, type: 'ui.resize', request_id: `r-${++sequence}`, height: Math.min(960, Math.max(520, document.documentElement.scrollHeight + 12)) }, '*');
  }

  $('settings-form').addEventListener('submit', async (event) => {
    event.preventDefault(); $('save').disabled = true; $('form-status').textContent = '正在保存…';
    try {
      await request('config.save', { config: { interval_minutes: Number($('interval').value), request_timeout_seconds: Number($('timeout').value), max_concurrency: Number($('concurrency').value), include_usage: $('usage').checked, mask_account_identity: $('mask-identity').checked } }, 30000);
      $('form-status').textContent = '设置已保存，正在刷新账号…';
      const response = await request('config.test', {}, 90000);
      const raw = response.result?.status_json;
      if (raw) renderStatus(JSON.parse(raw));
      $('form-status').textContent = '设置已保存并生效'; notify('success', '订阅监控设置已保存');
    } catch (error) { $('form-status').textContent = error.message; }
    finally { $('save').disabled = false; resize(); }
  });

  $('refresh').addEventListener('click', async () => {
    $('refresh').disabled = true; $('subtitle').textContent = '正在刷新所有账号…';
    try {
      const response = await request('config.test', {}, 90000);
      const raw = response.result?.status_json;
      if (raw) renderStatus(JSON.parse(raw));
    } catch (error) { notify('error', error.message); }
    finally { $('refresh').disabled = false; }
  });

  $('accounts').addEventListener('click', (event) => {
    const button = event.target.closest('.stats-button');
    if (!button) return;
    const account = latestAccounts.find((item) => Number(item.host_account_id) === Number(button.dataset.accountId));
    if (account) openAccountDetail(account);
  });

  $('detail-close').addEventListener('click', () => $('account-detail').close());
  $('account-detail').addEventListener('click', (event) => {
    if (event.target === $('account-detail')) $('account-detail').close();
  });

  parent.postMessage({ source: 'sub2api-plugin-ui', bridge_token: token, type: 'sub2api.plugin.ready', request_id: `ready-${++sequence}` }, '*');
  Promise.allSettled([loadConfig(), loadStatus()]).finally(resize);
  setInterval(loadStatus, 15000);
})();
