<script>
  import { onMount } from 'svelte'
  import { api } from '../lib/api.js'
  import { formatDate, formatTime, humanError } from '../lib/format.js'

  const STATUS = {
    pending_add: { label: 'Ожидает добавления', cls: 'badge-warn' },
    pending_update: { label: 'Ожидает изменения', cls: 'badge-warn' },
    pending_remove: { label: 'Ожидает удаления', cls: 'badge-cancel' }
  }

  /** @type {import('../lib/types.js').SyncQueueRow[]} */
  let items = $state([])
  let total = $state(0)
  let loading = $state(true)
  let error = $state('')
  let updatedAt = $state(null)
  // Строка, по которой уже запущена синхронизация: кнопка блокируется, чтобы
  // повторный клик не плодил параллельные прогоны.
  let syncing = $state(null)
  let syncError = $state('')

  let status = $state('')
  let limit = $state(50)
  let offset = $state(0)

  async function load() {
    loading = true
    error = ''
    const params = new URLSearchParams()
    params.set('limit', String(limit))
    params.set('offset', String(offset))
    if (status) params.set('status', status)

    try {
      const data = await api.get(`/admin/api/sync-queue?${params}`)
      items = data.items || []
      total = data.total || 0
      updatedAt = new Date().toISOString()
    } catch (err) {
      error = humanError(err)
    } finally {
      loading = false
    }
  }

  onMount(load)

  function resetAndLoad() {
    offset = 0
    load()
  }

  // Ручной прогон — существующий endpoint подписки; очередь только ссылается
  // на него, прямых UPDATE subscription_nodes нет.
  async function resync(row) {
    syncing = `${row.subscription_id}/${row.node_id}`
    syncError = ''
    try {
      await api.post(`/admin/api/subscriptions/${row.subscription_id}/sync`, {})
      await load()
    } catch (err) {
      syncError = humanError(err)
    } finally {
      syncing = null
    }
  }

  function statusMeta(row) {
    return STATUS[row.status] || { label: row.status, cls: 'badge-muted' }
  }

  function plural(n, forms) {
    const mod10 = n % 10
    const mod100 = n % 100
    if (mod10 === 1 && mod100 !== 11) return forms[0]
    if (mod10 >= 2 && mod10 <= 4 && (mod100 < 12 || mod100 > 14)) return forms[1]
    return forms[2]
  }

  // Возраст записи: главный сигнал застрявшей синхронизации.
  function formatAge(value) {
    if (!value) return '—'
    const diff = Date.now() - new Date(value).getTime()
    if (!(diff > 0)) return 'меньше минуты назад'
    const seconds = Math.floor(diff / 1000)
    if (seconds < 60) return 'меньше минуты назад'
    const days = Math.floor(seconds / 86400)
    const hours = Math.floor((seconds % 86400) / 3600)
    const minutes = Math.floor((seconds % 3600) / 60)
    const parts = []
    if (days) parts.push(`${days} ${plural(days, ['день', 'дня', 'дней'])}`)
    if (hours) parts.push(`${hours} ${plural(hours, ['час', 'часа', 'часов'])}`)
    if (minutes) parts.push(`${minutes} ${plural(minutes, ['минута', 'минуты', 'минут'])}`)
    return `${parts.slice(0, 2).join(' ')} назад`
  }

  function pageFirst() {
    return total === 0 ? 0 : offset + 1
  }

  function pageLast() {
    return Math.min(offset + limit, total)
  }
</script>

<div class="page-head">
  <div>
    <h1>Очередь синхронизации</h1>
    <p class="subtitle">
      Пары подписка↔нода, которые фоновый воркер ещё не применил. Только чтение:
      «Пересинхронизировать» запускает существующий сервисный прогон подписки.
    </p>
  </div>
  <div class="head-actions">
    {#if updatedAt}<span class="muted small">Обновлено в {formatTime(updatedAt)}</span>{/if}
    <button class="btn" onclick={load} disabled={loading}>Обновить</button>
  </div>
</div>

{#if error}
  <div class="notice notice-error">
    {error}
    <button class="btn btn-ghost" onclick={load}>Повторить</button>
  </div>
{/if}

{#if syncError}
  <div class="notice notice-error">{syncError}</div>
{/if}

<div class="panel">
  <div class="toolbar">
    <select bind:value={status} onchange={resetAndLoad}>
      <option value="">Вся очередь</option>
      {#each Object.entries(STATUS) as [value, meta] (value)}
        <option {value}>{meta.label}</option>
      {/each}
    </select>
    <span class="muted">в очереди {total}</span>
  </div>

  {#if loading}
    <div class="empty">Загрузка…</div>
  {:else if items.length === 0}
    <div class="empty">Очередь пуста — все привязки синхронизированы</div>
  {:else}
    <table>
      <thead>
        <tr>
          <th>ПОДПИСКА</th>
          <th>ПОЛЬЗОВАТЕЛЬ</th>
          <th>НОДА</th>
          <th>СТАТУС</th>
          <th class="num">ПОПЫТКИ</th>
          <th>ПОВТОР</th>
          <th>ВОЗРАСТ</th>
          <th>ОШИБКА</th>
          <th>ДЕЙСТВИЯ</th>
        </tr>
      </thead>
      <tbody>
        {#each items as row (`${row.subscription_id}/${row.node_id}`)}
          <tr>
            <td class="mono">
              <a class="row-link" href="#/subscriptions/{row.subscription_id}">#{row.subscription_id}</a>
              {#if row.subscription_id_public}
                <div class="muted mono small-id">{row.subscription_id_public}</div>
              {/if}
            </td>
            <td class="mono">{row.telegram_id}</td>
            <td>
              #{row.node_id}
              {#if row.node_name}<div class="muted">{row.node_name}</div>{/if}
            </td>
            <td><span class="badge {statusMeta(row).cls}">{statusMeta(row).label}</span></td>
            <td class="num mono">{row.retry_count}</td>
            <td>{formatDate(row.retry_at)}</td>
            <td title={formatDate(row.updated_at)}>{formatAge(row.updated_at)}</td>
            <td class="error-cell" title={row.last_error || ''}>
              {row.last_error || '—'}
            </td>
            <td>
              <button
                class="btn btn-ghost"
                onclick={() => resync(row)}
                disabled={syncing === `${row.subscription_id}/${row.node_id}`}
              >
                {syncing === `${row.subscription_id}/${row.node_id}` ? 'Синхронизация…' : 'Пересинхронизировать'}
              </button>
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
    <div class="pager">
      <span class="muted">{pageFirst()}–{pageLast()} из {total}</span>
      <button class="btn btn-ghost" onclick={() => { offset -= limit; load() }} disabled={offset === 0 || loading}>‹</button>
      <button class="btn btn-ghost" onclick={() => { offset += limit; load() }} disabled={offset + limit >= total || loading}>›</button>
      <select bind:value={limit} onchange={resetAndLoad}>
        <option value={50}>50</option>
        <option value={100}>100</option>
        <option value={200}>200</option>
      </select>
    </div>
  {/if}
</div>

<style>
  .head-actions {
    display: flex;
    align-items: center;
    gap: 10px;
  }

  .small-id {
    font-size: 11px;
  }

  .error-cell {
    max-width: 260px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    color: var(--danger);
  }

  .badge-cancel {
    background: var(--danger-soft);
    color: var(--danger);
  }

  .row-link {
    color: inherit;
    text-decoration: none;
  }

  .row-link:hover {
    color: var(--accent);
    text-decoration: underline;
  }
</style>
