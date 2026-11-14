<script>
  import { onMount } from 'svelte'
  import { api } from '../lib/api.js'
  import { formatDate, humanError } from '../lib/format.js'

  // Словарь статусов — локальная константа вью (см. 00-common.md).
  const STATUS = {
    active: { label: 'Активна', cls: 'badge-ok' },
    expired: { label: 'Истекла', cls: 'badge-muted' },
    paused: { label: 'Пауза', cls: 'badge-warn' },
    canceled: { label: 'Отменена', cls: 'badge-cancel' },
    revoked: { label: 'Отозвана', cls: 'badge-cancel' }
  }

  /** @type {import('../lib/types.js').Subscription[]} */
  let items = $state([])
  let total = $state(0)
  let loading = $state(true)
  let error = $state('')

  let status = $state('')
  let planID = $state('')
  // Часы для фильтра expiring_in: '' = без фильтра, иначе строка часов
  // (24/168/336/720) — сервер ждёт положительное число часов.
  let expiring = $state('')
  let qInput = $state('')
  let qApplied = $state('')
  let sort = $state('id')
  let order = $state('desc')
  let limit = $state(50)
  let offset = $state(0)

  /** @type {import('../lib/types.js').Plan[]} */
  let plans = $state([])

  // Подписи окна истечения — для примечания под фильтром.
  const EXPIRING_LABELS = { 24: '24 ч', 168: '7 дней', 336: '14 дней', 720: '30 дней' }

  let debounceTimer

  async function load() {
    loading = true
    error = ''
    const params = new URLSearchParams()
    params.set('limit', String(limit))
    params.set('offset', String(offset))
    if (status) params.set('status', status)
    if (planID) params.set('plan_id', planID)
    if (qApplied) params.set('q', qApplied)
    if (expiring) params.set('expiring_in', expiring)
    params.set('sort', sort)
    params.set('order', order)

    try {
      const data = await api.get(`/admin/api/subscriptions?${params}`)
      items = data.items || []
      total = data.total || 0
    } catch (err) {
      error = humanError(err)
    } finally {
      loading = false
    }
  }

  async function loadPlans() {
    try {
      const data = await api.get('/admin/api/plans')
      plans = data.items || []
    } catch {
      plans = []
    }
  }

  onMount(() => {
    // Карточка «Истекают» на «Состоянии» кладёт предустановку сюда; флаг
    // одноразовый: применяется один раз и снимается.
    const preset = sessionStorage.getItem('expiring_in')
    if (preset) {
      expiring = preset
      sessionStorage.removeItem('expiring_in')
    }
    // Глобальный поиск кладёт предустановку сюда; ключ одноразовый.
    const search = sessionStorage.getItem('admin_search')
    if (search) {
      sessionStorage.removeItem('admin_search')
      try {
        const parsed = JSON.parse(search)
        if (parsed.screen === 'subscriptions' && parsed.q) {
          qInput = parsed.q
          qApplied = parsed.q
        }
      } catch {
        // мусор в хранилище — игнорируем
      }
    }
    loadPlans()
    load()
  })

  function resetAndLoad() {
    offset = 0
    load()
  }

  function resetFilters() {
    status = ''
    planID = ''
    expiring = ''
    qInput = ''
    qApplied = ''
    sort = 'id'
    order = 'desc'
    resetAndLoad()
  }

  // Поиск — серверный, с debounce 300 мс.
  function scheduleSearch() {
    clearTimeout(debounceTimer)
    debounceTimer = setTimeout(() => {
      const value = qInput.trim()
      if (value === qApplied) return
      qApplied = value
      resetAndLoad()
    }, 300)
  }

  function sortBy(column) {
    if (sort === column) {
      order = order === 'desc' ? 'asc' : 'desc'
    } else {
      sort = column
      order = 'desc'
    }
    resetAndLoad()
  }

  function pageFirst() {
    return total === 0 ? 0 : offset + 1
  }

  function pageLast() {
    return Math.min(offset + limit, total)
  }

  function statusMeta(row) {
    return STATUS[row.status] || { label: row.status, cls: 'badge-muted' }
  }

  // Истекает: пусто или истекшая подписка — прочерк; близкий срок — красным.
  function expiringSoon(row) {
    return (
      row.status !== 'expired' &&
      row.expires_at &&
      new Date(row.expires_at).getTime() - Date.now() < 24 * 3600 * 1000
    )
  }

  // Узлы: «2/2», а при зависших удалениях — «2/2 · −1» с подсказкой.
  function nodesLabel(row) {
    const pendingRemove = row.nodes_total - row.nodes_active
    return pendingRemove > 0 ? `${row.nodes_active}/${row.nodes_total} · −${pendingRemove}` : `${row.nodes_active}/${row.nodes_total}`
  }
</script>

<div class="page-head">
  <div>
    <h1>Подписки</h1>
    <p class="subtitle">
      Состояние доступа пользователей. Мутации — только через сервисный слой:
      продление, сброс трафика, отзыв и удаление — в карточке подписки.
    </p>
  </div>
  <button class="btn" onclick={load} disabled={loading}>Обновить</button>
</div>

{#if error}
  <div class="notice notice-error">
    {error}
    <button class="btn btn-ghost" onclick={load}>Повторить</button>
  </div>
{/if}

<div class="panel">
  <div class="toolbar">
    <input
      class="search"
      type="search"
      placeholder="telegram_id, username или subscription_id"
      bind:value={qInput}
      oninput={scheduleSearch}
    />
    <select bind:value={status} onchange={resetAndLoad}>
      <option value="">Все статусы</option>
      {#each Object.entries(STATUS) as [value, meta] (value)}
        <option {value}>{meta.label}</option>
      {/each}
    </select>
    <select bind:value={planID} onchange={resetAndLoad}>
      <option value="">Все планы</option>
      {#each plans as plan (plan.id)}
        <option value={String(plan.id)}>{plan.name}</option>
      {/each}
    </select>
    <label class="check">
      <span>истекают за</span>
      <select bind:value={expiring} onchange={resetAndLoad} aria-label="Окно истечения">
        <option value="">Без фильтра</option>
        <option value="24">24 ч</option>
        <option value="168">7 дней</option>
        <option value="336">14 дней</option>
        <option value="720">30 дней</option>
      </select>
    </label>
    <span class="muted">найдено {total}</span>
  </div>

  {#if expiring}
    <div class="muted expiring-note">истекают в ближайшие {EXPIRING_LABELS[expiring] || `${expiring} ч`}</div>
  {/if}

  {#if loading}
    <div class="empty">Загрузка…</div>
  {:else if items.length === 0}
    <div class="empty">
      Подписок не найдено
      <button class="btn btn-ghost" onclick={resetFilters}>Сбросить фильтры</button>
    </div>
  {:else}
    <table>
      <thead>
        <tr>
          <th class="sortable" onclick={() => sortBy('id')}>ID{sort === 'id' ? (order === 'desc' ? ' ↓' : ' ↑') : ''}</th>
          <th>ПОЛЬЗОВАТЕЛЬ</th>
          <th>ПЛАН</th>
          <th>СТАТУС</th>
          <th class="sortable" onclick={() => sortBy('expires_at')}>ИСТЕКАЕТ{sort === 'expires_at' ? (order === 'desc' ? ' ↓' : ' ↑') : ''}</th>
          <th class="sortable" onclick={() => sortBy('last_request')}>ПОСЛЕДНИЙ ЗАПРОС{sort === 'last_request' ? (order === 'desc' ? ' ↓' : ' ↑') : ''}</th>
          <th class="num">УЗЛЫ</th>
          <th>ДЕЙСТВИЯ</th>
        </tr>
      </thead>
      <tbody>
        {#each items as row (row.id)}
          <tr>
            <td class="mono muted">
              <a class="row-link" href="#/subscriptions/{row.id}">{row.id}</a>
            </td>
            <td>
              {#if row.username}
                @{row.username}
                <div class="muted mono">{row.telegram_id}</div>
              {:else}
                <span class="mono">{row.telegram_id}</span>
              {/if}
            </td>
            <td>{row.plan_name || '—'}</td>
            <td><span class="badge {statusMeta(row).cls}">{statusMeta(row).label}</span></td>
            <td>
              {#if row.status === 'expired' || !row.expires_at}
                —
              {:else}
                <span class:danger={expiringSoon(row)}>{formatDate(row.expires_at)}</span>
              {/if}
            </td>
            <td>{formatDate(row.last_request)}</td>
            <td class="num mono" title={row.nodes_total - row.nodes_active > 0 ? 'есть узлы в pending_remove' : ''}>
              {nodesLabel(row)}
            </td>
            <td>
              <a class="btn btn-ghost" href="#/subscriptions/{row.id}">Открыть</a>
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
  .check {
    display: flex;
    align-items: center;
    gap: 6px;
    white-space: nowrap;
  }

  .expiring-note {
    padding: 8px 14px 0;
  }

  .danger {
    color: var(--danger);
    font-weight: 600;
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