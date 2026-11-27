<script>
  import { onMount } from 'svelte'
  import { api } from '../lib/api.js'
  import { formatDate, formatMoney, formatTime, humanError } from '../lib/format.js'

  // Словарь статусов — локальная константа вью: форматирование сущности — не
  // забота общего format.js (см. ТЗ 07 §7).
  const STATUS = {
    pending: { label: 'Ожидает оплаты', cls: 'badge-warn' },
    paid: { label: 'Оплачен', cls: 'badge-ok' },
    expired: { label: 'Истёк', cls: 'badge-muted' },
    canceled: { label: 'Отменён', cls: 'badge-cancel' }
  }

  /** @type {import('../lib/types.js').Order[]} */
  let items = $state([])
  let total = $state(0)
  let loading = $state(true)
  let error = $state('')

  let status = $state('')
  let qInput = $state('')
  let qApplied = $state('')
  // Точный id из глобального поиска (order:42): отдельный read-only фильтр,
  // живёт до ручного сброса — так видно, что список сужен именно id.
  let idFilter = $state('')
  let from = $state('')
  let to = $state('')
  let sort = $state('created_at')
  let order = $state('desc')
  let limit = $state(50)
  let offset = $state(0)

  let debounceTimer
  let updatedAt = $state(null)

  async function load() {
    loading = true
    error = ''
    const params = new URLSearchParams()
    params.set('limit', String(limit))
    params.set('offset', String(offset))
    if (idFilter) params.set('id', idFilter)
    if (status) params.set('status', status)
    if (qApplied) params.set('q', qApplied)
    if (from) params.set('from', from)
    if (to) params.set('to', to)
    params.set('sort', sort)
    params.set('order', order)

    try {
      const data = await api.get(`/admin/api/orders?${params}`)
      items = data.items || []
      total = data.total || 0
      updatedAt = new Date().toISOString()
    } catch (err) {
      error = humanError(err)
    } finally {
      loading = false
    }
  }

  onMount(() => {
    // Глобальный поиск кладёт предустановку сюда; ключ одноразовый.
    const search = sessionStorage.getItem('admin_search')
    if (search) {
      sessionStorage.removeItem('admin_search')
      try {
        const preset = JSON.parse(search)
        if (preset.screen === 'orders' && preset.q) {
          // order:42 несёт точный id: фильтруем только им — q («42») вместе с
          // id дал бы пересечение «точный id И похоже на 42» и пустой список.
          if (preset.id) {
            idFilter = preset.id
            qInput = preset.q
          } else {
            qInput = preset.q
            qApplied = preset.q
          }
        }
      } catch {
        // мусор в хранилище — игнорируем
      }
    }
    load()
  })

  function resetAndLoad() {
    offset = 0
    load()
  }

  function resetFilters() {
    status = ''
    qInput = ''
    qApplied = ''
    idFilter = ''
    from = ''
    to = ''
    sort = 'created_at'
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

  // Стрелка сортировки живёт рядом с заголовком, а не в цикле по списку
  // колонок: sortable-колонки разнесены по таблице (ID, СУММА, СОЗДАН, ОПЛАЧЕН).
  function sortArrow(column) {
    if (sort !== column) return ''
    return order === 'desc' ? ' ↓' : ' ↑'
  }

  function pageFirst() {
    return total === 0 ? 0 : offset + 1
  }

  function pageLast() {
    return Math.min(offset + limit, total)
  }

  function hasPrev() {
    return offset > 0
  }

  function hasNext() {
    return offset + limit < total
  }

  function prevPage() {
    offset -= limit
    load()
  }

  function nextPage() {
    offset += limit
    load()
  }

  // Списано отличается от суммы — провайдер удержал комиссию: подсветка.
  function feeDiffers(row) {
    return row.callback_amount_cents !== null && row.callback_amount_cents !== row.amount_cents
  }

  function statusMeta(row) {
    return STATUS[row.status] || { label: row.status, cls: 'badge-muted' }
  }
</script>

<div class="page-head">
  <div>
    <h1>
      Заказы
      <span class="badge badge-muted">только чтение</span>
      {#if idFilter}<span class="badge badge-warn">id: {idFilter}</span>{/if}
    </h1>
    <p class="subtitle">
      Разбор платежей. Статусы меняются только платёжными транзакциями бота — вручную их
      править нельзя, чтобы не разойтись с провайдером.
    </p>
  </div>
  <div class="head-actions">
    {#if updatedAt}<span class="muted refreshed">Обновлено в {formatTime(updatedAt)}</span>{/if}
    <button class="btn" onclick={load} disabled={loading}>Обновить</button>
  </div>
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
      placeholder="telegram_id или ID платежа"
      bind:value={qInput}
      oninput={scheduleSearch}
    />
    <select bind:value={status} onchange={resetAndLoad}>
      <option value="">Все статусы</option>
      {#each Object.entries(STATUS) as [value, meta] (value)}
        <option {value}>{meta.label}</option>
      {/each}
    </select>
    <input class="date" type="date" bind:value={from} onchange={resetAndLoad} />
    <span class="muted">—</span>
    <input class="date" type="date" bind:value={to} onchange={resetAndLoad} />
    <span class="muted">найдено {total}</span>
  </div>

  {#if loading}
    <div class="empty">Загрузка…</div>
  {:else if items.length === 0}
    <div class="empty">
      Заказы не найдены
      <button class="btn btn-ghost" onclick={resetFilters}>Сбросить фильтры</button>
    </div>
  {:else}
    <table>
      <thead>
        <tr>
          <th class="sortable" onclick={() => sortBy('id')}>ID{sortArrow('id')}</th>
          <th>ПОЛЬЗОВАТЕЛЬ</th>
          <th>ПРОДУКТ</th>
          <th>ПЛАН</th>
          <th>СТАТУС</th>
          <th class="sortable num" onclick={() => sortBy('amount_cents')}>СУММА{sortArrow('amount_cents')}</th>
          <th class="num">СПИСАНО</th>
          <th>ПРОВАЙДЕР</th>
          <th class="sortable" onclick={() => sortBy('created_at')}>СОЗДАН{sortArrow('created_at')}</th>
          <th class="sortable" onclick={() => sortBy('paid_at')}>ОПЛАЧЕН{sortArrow('paid_at')}</th>
        </tr>
      </thead>
      <tbody>
        {#each items as row (row.id)}
          <tr>
            <td class="mono muted">
              <a class="row-link" href="#/orders/{row.id}">{row.id}</a>
            </td>
            <td><a class="row-link mono" href="#/subscriptions/{row.subscription_id}">@{row.telegram_id}</a></td>
            <td>{row.product_name}</td>
            <td>{row.plan_name}</td>
            <td><span class="badge {statusMeta(row).cls}">{statusMeta(row).label}</span></td>
            <td class="num">{formatMoney(row.amount_cents, row.currency)}</td>
            <td class="num">
              <span class:fee-diff={feeDiffers(row)}>
                {formatMoney(row.callback_amount_cents, row.currency)}
              </span>
            </td>
            <td class="muted">{row.payment_provider || '—'}</td>
            <td>{formatDate(row.created_at)}</td>
            <td>{formatDate(row.paid_at)}</td>
          </tr>
        {/each}
      </tbody>
    </table>
    <div class="pager">
      <span class="muted">{pageFirst()}–{pageLast()} из {total}</span>
      <button class="btn btn-ghost" onclick={prevPage} disabled={!hasPrev() || loading}>‹</button>
      <button class="btn btn-ghost" onclick={nextPage} disabled={!hasNext() || loading}>›</button>
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

  .refreshed {
    font-size: 12px;
  }

  .date {
    padding: 7px 10px;
    border: 1px solid var(--border);
    border-radius: 8px;
    background: var(--bg);
  }

  .badge-cancel {
    background: var(--danger-soft);
    color: var(--danger);
  }

  .fee-diff {
    color: var(--warn);
    font-weight: 600;
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
