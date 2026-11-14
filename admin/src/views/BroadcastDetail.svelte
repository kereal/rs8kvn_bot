<script>
  import { onMount } from 'svelte'
  import { api } from '../lib/api.js'
  import { formatDate, humanError } from '../lib/format.js'
  import Modal from '../lib/Modal.svelte'

  // Словарь статусов — локальная константа вью.
  const STATUS = {
    scheduled: { label: 'Запланирована', cls: 'badge-warn' },
    running: { label: 'Идёт', cls: 'badge-ok' },
    completed: { label: 'Завершена', cls: 'badge-muted' },
    failed: { label: 'Провалилась', cls: 'badge-fail' },
    canceled: { label: 'Отменена', cls: 'badge-cancel' }
  }

  let { id } = $props()

  /** @type {import('../lib/types.js').BroadcastDetail|null} */
  let row = $state(null)
  let loading = $state(true)
  let error = $state('')
  let notice = $state('')
  let warn = $state('')

  // Диалог деактивации заблокировавших: обязательное подтверждение
  // с точными числами blocked/unreachable (§3, §7).
  let deactivateOpen = $state(false)
  let deactivating = $state(false)

  async function load() {
    loading = true
    error = ''
    try {
      row = await api.get(`/admin/api/broadcasts/${id}`)
    } catch (err) {
      error = humanError(err)
    } finally {
      loading = false
    }
  }

  onMount(load)

  function statusMeta() {
    return STATUS[row.status] || { label: row.status, cls: 'badge-muted' }
  }

  function progressPercent() {
    if (!row.recipients_total) return 0
    return Math.min(100, Math.round((row.sent_count / row.recipients_total) * 100))
  }

  function canCancel() {
    return row.status === 'scheduled' || row.status === 'running'
  }
  function canRetry() {
    return row.status === 'failed'
  }
  function canDeactivate() {
    return (row.status === 'completed' || row.status === 'failed') && row.blocked_count + row.unreachable_count > 0
  }
  function readOnly() {
    return row.status === 'completed' || row.status === 'canceled'
  }

  async function cancelBroadcast() {
    const ok = confirm(`Отменить рассылку #${row.id} «${row.name}»?`)
    if (!ok) return
    error = ''
    try {
      row = await api.post(`/admin/api/broadcasts/${row.id}/cancel`)
      notice = 'Рассылка отменена'
    } catch (err) {
      error = humanError(err)
    }
  }

  async function retryBroadcast() {
    const ok = confirm(`Повторить доставку ошибок рассылки #${row.id}?`)
    if (!ok) return
    error = ''
    try {
      row = await api.post(`/admin/api/broadcasts/${row.id}/retry`)
      notice = 'Повтор поставлен в очередь'
    } catch (err) {
      error = humanError(err)
    }
  }

  // Частичный сбой — предупреждение, а не успех: service возвращает
  // {targets, revoked, failed} и, при сбоях, поле error (§3).
  async function deactivateBlocked() {
    deactivateOpen = false
    deactivating = true
    error = ''
    warn = ''
    notice = ''
    try {
      const result = await api.post(`/admin/api/broadcasts/${row.id}/deactivate-blocked`)
      if (result.failed > 0 || result.error) {
        warn = `Выключено ${result.revoked} из ${result.targets}; ошибок: ${result.failed}${result.error ? ' — ' + result.error : ''}`
      } else {
        notice = `Выключено подписок: ${result.revoked} (статус revoked, снятие VPN-доступа поставлено в очередь)`
      }
      await load()
    } catch (err) {
      error = humanError(err)
    } finally {
      deactivating = false
    }
  }
</script>

<a class="back" href="#/broadcasts">← К рассылкам</a>

{#if loading}
  <div class="empty">Загрузка…</div>
{:else if error}
  <div class="notice notice-error">
    {error}
    <button class="btn btn-ghost" onclick={load}>Повторить</button>
  </div>
{:else}
  {@const report = /** @type {any} */ (row)}
  <div class="page-head">
    <div>
      <h1>
        Рассылка #{row.id}
        <span class="badge {statusMeta().cls}" title={row.last_error || undefined}>{statusMeta().label}</span>
      </h1>
      <p class="subtitle">{row.name}</p>
    </div>
    <div class="actions-bar">
      {#if canCancel()}
        <button class="btn" onclick={cancelBroadcast}>Отменить</button>
      {/if}
      {#if canRetry()}
        <button class="btn" onclick={retryBroadcast}>Повторить ошибки</button>
      {/if}
      {#if canDeactivate()}
        <button class="btn btn-danger" onclick={() => (deactivateOpen = true)} disabled={deactivating}>
          Деактивировать заблокировавших
        </button>
      {/if}
    </div>
  </div>

  {#if readOnly()}
    <div class="notice notice-ok" style="margin-bottom:12px">Рассылка завершена — только чтение</div>
  {/if}
  {#if notice}
    <div class="notice notice-ok">{notice}</div>
  {/if}
  {#if warn}
    <div class="notice notice-error">⚠️ {warn}</div>
  {/if}
  {#if error}
    <div class="notice notice-error">{error}</div>
  {/if}

  <div class="cards">
    <section class="panel card">
      <h2>Прогресс</h2>
      <div class="progress" title={`${row.sent_count} из ${row.recipients_total}`}>
        <div class="progress-bar" style={`width:${progressPercent()}%`}></div>
      </div>
      <p class="muted progress-label">{row.sent_count} / {row.recipients_total} доставлено</p>
      <dl>
        <dt>Доставлено</dt>
        <dd>{row.sent_count}</dd>
        <dt>Заблокировали бота</dt>
        <dd>{row.blocked_count}</dd>
        <dt>Недоступны</dt>
        <dd>{row.unreachable_count}</dd>
        <dt>Ошибок</dt>
        <dd>{row.failed_count}</dd>
      </dl>
      {#if row.last_error}
        <p class="last-error">Последняя ошибка: {row.last_error}</p>
      {/if}
    </section>

    <section class="panel card">
      <h2>Аудитория</h2>
      <dl>
        <dt>Фильтр</dt>
        <dd>{row.filters_text || '—'}</dd>
        <dt>Запланировано</dt>
        <dd>{row.planned_at ? formatDate(row.planned_at) : 'сразу'}</dd>
        <dt>Начало</dt>
        <dd>{formatDate(row.started_at)}</dd>
        <dt>Завершение</dt>
        <dd>{formatDate(row.finished_at)}</dd>
        <dt>Повторов</dt>
        <dd>{row.retry_count}</dd>
        <dt>Снапшот аудитории</dt>
        <dd>{row.snapshot_taken ? 'снят' : 'не снят'}</dd>
      </dl>
    </section>

    <section class="panel card card-text">
      <h2>Текст</h2>
      <pre class="mono">{row.message_text}</pre>
    </section>
  </div>

  <section class="panel errors">
    <h2>Не доставлено</h2>
    {#if report.blocked?.length === 0 && report.unreachable?.length === 0 && (report.errors?.length ?? 0) === 0}
      <p class="muted">Все получатели доставлены, проблем нет</p>
    {:else}
      <p class="muted undelivered-count">Доставлено: {report.delivered_count ?? row.sent_count}</p>
      {#if report.blocked?.length}
        <h3>Заблокировали бота ({report.blocked.length})</h3>
        <div class="id-list">
          {#each report.blocked as tid (tid)}
            <span class="mono id-chip">{tid}</span>
          {/each}
        </div>
      {/if}
      {#if report.unreachable?.length}
        <h3>Недоступны ({report.unreachable.length})</h3>
        <div class="id-list">
          {#each report.unreachable as tid (tid)}
            <span class="mono id-chip">{tid}</span>
          {/each}
        </div>
      {/if}
      {#if report.errors?.length}
        <h3>Ошибки ({report.errors.length})</h3>
        <table>
          <thead>
            <tr>
              <th>TELEGRAM ID</th>
              <th>ПРИЧИНА</th>
            </tr>
          </thead>
          <tbody>
            {#each report.errors as item (item.telegram_id)}
              <tr>
                <td class="mono">{item.telegram_id}</td>
                <td>{item.error}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      {/if}
    {/if}
  </section>
{/if}

<Modal bind:open={deactivateOpen} title="Деактивация заблокировавших">
  <p class="confirm-text">
    Выключить подписки пользователей, заблокировавших бота или недоступных
    (рассылка #{row?.id})?
  </p>
  <dl class="deactivate-counts">
    <dt>Заблокировали</dt>
    <dd>{row?.blocked_count ?? 0}</dd>
    <dt>Недоступны</dt>
    <dd>{row?.unreachable_count ?? 0}</dd>
  </dl>
  <p class="hint">
    Подписки переводятся в статус «revoked», VPN-доступ снимается, пользователи
    удаляются из рассылок. Действие необратимо.
  </p>
  {#snippet footer()}
    <button class="btn" onclick={() => (deactivateOpen = false)}>Отмена</button>
    <button class="btn btn-danger" onclick={deactivateBlocked} disabled={deactivating}>
      {deactivating ? 'Выключаем…' : 'Да, выключить'}
    </button>
  {/snippet}
</Modal>

<style>
  .back {
    display: inline-block;
    margin-bottom: 12px;
    color: var(--muted);
    text-decoration: none;
  }

  .back:hover {
    color: var(--accent);
  }

  .actions-bar {
    display: flex;
    gap: 8px;
  }

  .cards {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(280px, 1fr));
    gap: 14px;
  }

  .card {
    padding: 14px 16px;
  }

  .card h2,
  .errors h2 {
    margin: 0 0 10px;
    font-size: 13px;
    letter-spacing: 0.02em;
    text-transform: uppercase;
    color: var(--muted);
  }

  dl {
    margin: 0;
    display: grid;
    grid-template-columns: max-content 1fr;
    gap: 6px 16px;
  }

  dt {
    color: var(--muted);
  }

  dd {
    margin: 0;
  }

  .progress {
    width: 100%;
    height: 8px;
    border-radius: 4px;
    background: color-mix(in srgb, var(--border) 60%, transparent);
    overflow: hidden;
  }

  .progress-bar {
    height: 100%;
    background: var(--accent);
    transition: width 0.3s ease;
  }

  .progress-label {
    margin: 6px 0 10px;
    font-size: 12.5px;
  }

  .last-error {
    margin: 10px 0 0;
    color: var(--danger);
    font-size: 12.5px;
  }

  .card-text {
    grid-column: 1 / -1;
  }

  .card-text pre {
    margin: 0;
    white-space: pre-wrap;
    word-break: break-word;
    max-height: 320px;
    overflow: auto;
  }

  .errors {
    margin-top: 14px;
    padding: 14px 16px;
  }

  .undelivered-count {
    margin: 0 0 8px;
  }

  .errors h3 {
    margin: 12px 0 6px;
    font-size: 12.5px;
    color: var(--text);
  }

  .id-list {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
  }

  .id-chip {
    background: color-mix(in srgb, var(--border) 40%, transparent);
    border-radius: 4px;
    padding: 2px 6px;
  }

  .badge-fail,
  .badge-cancel {
    background: var(--danger-soft);
    color: var(--danger);
  }

  .confirm-text {
    margin: 0 0 10px;
  }

  .deactivate-counts {
    margin: 0 0 10px;
  }
</style>
