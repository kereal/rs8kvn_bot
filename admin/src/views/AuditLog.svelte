<script>
  import { onMount } from 'svelte'
  import { api } from '../lib/api.js'
  import { formatDate, humanError } from '../lib/format.js'

  // Русские подписи — локальный словарь экрана (spec 07 §7): опции select
  // приходят с сервера (/admin/api/audit/events), подписи — отсюда. Новое
  // событие, которого ещё нет в словаре, показывается своим кодом моноширинно.
  const EVENT_LABELS = {
    subscription_created: 'Подписка создана',
    trial_created: 'Пробная подписка создана',
    trial_bound: 'Пробная подписка привязана',
    subscription_reanimated: 'Подписка восстановлена',
    subscription_renewed: 'Подписка продлена',
    plan_changed: 'Сменён тариф',
    subscription_expired: 'Подписка истекла',
    downgraded_to_free: 'Понижение до free',
    subscription_revoked: 'Доступ отозван',
    subscription_deleted: 'Подписка удалена',
    trial_deleted: 'Пробная подписка удалена',
    reminder_sent: 'Отправлено напоминание',
    traffic_notified: 'Отправлено трафик-уведомление',
    traffic_reset: 'Сброшен трафик подписки',
    order_created: 'Заказ создан',
    payment_confirmed: 'Оплата подтверждена',
    order_expired: 'Заказ истёк',
    order_canceled: 'Заказ отменён',
    chargeback_received: 'Получен чарджбэк',
    payment_creation_uncertain: 'Неопределённый исход платежа',
    broadcast_started: 'Рассылка запущена',
    broadcast_completed: 'Рассылка завершена',
    broadcast_failed: 'Рассылка провалилась',
    broadcast_canceled: 'Рассылка отменена',
    broadcast_created: 'Рассылка создана',
    broadcast_updated: 'Рассылка изменена',
    broadcast_retried: 'Повтор рассылки',
    broadcast_deleted: 'Рассылка удалена',
    node_created: 'Узел добавлен',
    node_updated: 'Узел изменён',
    node_deleted: 'Узел удалён',
    plan_nodes_changed: 'Состав узлов тарифа изменён',
    sync_run_degraded: 'Синк прошёл с отказами',
    orphaned_clients_revoked: 'Осиротевшие клиенты отозваны',
    product_created: 'Продукт создан',
    product_updated: 'Продукт изменён',
    product_deleted: 'Продукт удалён',
    invite_created: 'Инвайт создан',
    invite_deleted: 'Инвайт удалён'
  }

  const ENTITY_LABELS = {
    subscription: 'подписка',
    order: 'заказ',
    broadcast: 'рассылка',
    plan: 'план',
    node: 'нода',
    product: 'продукт',
    system: 'система'
  }

  const ACTOR_LABELS = {
    user: 'пользователь',
    admin: 'админ',
    worker: 'воркер',
    payment_provider: 'платёжный провайдер',
    system: 'система'
  }

  /** @type {import('../lib/types.js').AuditEntry[]} */
  let items = $state([])
  let total = $state(0)
  let loading = $state(true)
  let error = $state('')

  /** @type {string[]} */
  let events = $state([])
  /** @type {string[]} */
  let entityTypes = $state([])
  /** @type {string[]} */
  let actorTypes = $state([])

  let filterEvent = $state('')
  let filterEntityType = $state('')
  let filterActorType = $state('')
  let filterEntityId = $state('')
  let fromDate = $state('')
  let toDate = $state('')
  // Порядок по умолчанию — новые сверху (spec 07 §7); смена фильтров его
  // не сбрасывает, а переключение на asc — явное действие пользователя.
  let newestFirst = $state(true)
  let limit = $state(50)
  let offset = $state(0)

  // Раскрытие: разворачивается одна строка; ответ /admin/api/audit/{id}
  // кэшируется на время сессии списка — повторное раскрытие не рефетчит.
  let expandedId = $state(null)
  let detailsCache = $state({})

  function eventLabel(value) {
    return EVENT_LABELS[value] || value
  }

  function entityLabel(value) {
    return ENTITY_LABELS[value] || value
  }

  function actorLabel(value) {
    return ACTOR_LABELS[value] || value
  }

  // null в old/new_state расшифровывается по семантике миграции 039.
  function stateMeaning(event) {
    if (event.endsWith('_deleted')) return 'Сущность удалена — полей не осталось'
    if (event.endsWith('_created')) return 'Сущность создана — полей до этого не было'
    return 'Поля не менялись'
  }

  function detailsText(details) {
    if (details === null || details === undefined) return ''
    if (typeof details === 'string') return details
    if (typeof details !== 'object') return String(details)
    const keys = Object.keys(details)
    if (keys.length === 0) return ''
    return JSON.stringify(details)
  }

  function stateText(value) {
    if (value === null || value === undefined) return null
    if (typeof value === 'string') {
      try {
        return JSON.stringify(JSON.parse(value), null, 2)
      } catch {
        return value
      }
    }
    return JSON.stringify(value, null, 2)
  }

  // ---- данные --------------------------------------------------------------
  async function loadRegistry() {
    try {
      const data = await api.get('/admin/api/audit/events')
      events = data.events || []
      entityTypes = data.entity_types || []
      actorTypes = data.actor_types || []
    } catch (err) {
      // Реестр нужен только фильтрам: список продолжит работать, коды событий
      // останутся видны как есть.
      console.warn('audit events registry failed', err)
    }
  }

  async function load() {
    loading = true
    error = ''
    try {
      const query = new URLSearchParams()
      query.set('limit', String(limit))
      query.set('offset', String(offset))
      query.set('sort', 'created_at')
      query.set('order', newestFirst ? 'desc' : 'asc')
      if (filterEvent) query.set('event', filterEvent)
      if (filterEntityType) query.set('entity_type', filterEntityType)
      if (filterActorType) query.set('actor_type', filterActorType)
      const entityID = filterEntityId.trim()
      if (entityID) query.set('entity_id', entityID)
      if (fromDate) query.set('from', fromDate)
      if (toDate) query.set('to', toDate)

      const data = await api.get(`/admin/api/audit?${query.toString()}`)
      items = data.items || []
      total = data.total || 0
    } catch (err) {
      error = humanError(err)
    } finally {
      loading = false
    }
  }

  // Смена фильтров/размера страницы возвращает к первой странице и сохраняет
  // выбранный порядок (desc по умолчанию, spec 07 §7).
  function applyFilters() {
    offset = 0
    load()
  }

  function resetFilters() {
    filterEvent = ''
    filterEntityType = ''
    filterActorType = ''
    filterEntityId = ''
    fromDate = ''
    toDate = ''
    newestFirst = true
    limit = 50
    offset = 0
    load()
  }

  async function toggleRow(row) {
    if (expandedId === row.id) {
      expandedId = null
      return
    }

    expandedId = row.id
    if (detailsCache[row.id]) return

    try {
      detailsCache = { ...detailsCache, [row.id]: await api.get(`/admin/api/audit/${row.id}`) }
    } catch (err) {
      // Раскрытие только для чтения: ошибка не роняет список.
      error = humanError(err)
    }
  }

  function expandedDetail(id) {
    return detailsCache[id] || null
  }

  async function copyState(id, key) {
    const detail = detailsCache[id]
    if (!detail) return
    const text = stateText(detail[key])
    if (text === null) return
    try {
      await navigator.clipboard.writeText(text)
    } catch {
      // Буфер обмена недоступен — молча игнорируем, кнопка не критична.
    }
  }

  onMount(() => {
    loadRegistry()
    load()
  })
</script>

<div class="page-head">
  <div>
    <h1>Журнал аудита</h1>
    <p class="subtitle">Кто, когда и что менял. Экран только для чтения.</p>
  </div>
  <span class="badge-ro">только чтение</span>
</div>

{#if error}
  <div class="notice notice-error">
    {error}
    <button class="btn btn-ghost" onclick={load}>Повторить</button>
  </div>
{/if}

<div class="panel">
  <div class="toolbar">
    <select bind:value={filterEvent} onchange={applyFilters} aria-label="Тип события">
      <option value="">Все события</option>
      {#each events as ev (ev)}
        <option value={ev}>{eventLabel(ev)}</option>
      {/each}
    </select>
    <select bind:value={filterEntityType} onchange={applyFilters} aria-label="Сущность">
      <option value="">Все сущности</option>
      {#each entityTypes as et (et)}
        <option value={et}>{entityLabel(et)}</option>
      {/each}
    </select>
    <select bind:value={filterActorType} onchange={applyFilters} aria-label="Исполнитель">
      <option value="">Все исполнители</option>
      {#each actorTypes as at (at)}
        <option value={at}>{actorLabel(at)}</option>
      {/each}
    </select>
    <input
      type="text"
      placeholder="ID сущности"
      bind:value={filterEntityId}
      onkeydown={(e) => e.key === 'Enter' && applyFilters()}
      aria-label="ID сущности"
    />
    <input type="date" bind:value={fromDate} onchange={applyFilters} aria-label="С даты" />
    <input type="date" bind:value={toDate} onchange={applyFilters} aria-label="По дату" />
    <select bind:value={newestFirst} onchange={applyFilters} aria-label="Порядок">
      <option value={true}>Сначала новые</option>
      <option value={false}>Сначала старые</option>
    </select>
    <button class="btn btn-primary" onclick={() => load()}>Обновить</button>
  </div>

  {#if loading}
    <div class="empty">Загрузка…</div>
  {:else if items.length === 0}
    <div class="empty">
      Записей не найдено
      <button class="btn btn-ghost" onclick={resetFilters}>Сбросить фильтры</button>
    </div>
  {:else}
    <table>
      <thead>
        <tr>
          <th>Время</th>
          <th>Событие</th>
          <th>Сущность</th>
          <th>ID сущности</th>
          <th>Исполнитель</th>
          <th>Детали</th>
        </tr>
      </thead>
      <tbody>
        {#each items as row (row.id)}
          <tr class="row" class:expanded={expandedId === row.id} onclick={() => toggleRow(row)}>
            <td class="nowrap">{formatDate(row.created_at)}</td>
            <td class="mono" title={row.event}>{eventLabel(row.event)}</td>
            <td>
              {entityLabel(row.entity_type)}{row.entity_id ? ` #${row.entity_id}` : ''}
            </td>
            <td class="mono">{row.entity_id ?? '—'}</td>
            <td><span class="actor-badge actor-{row.actor_type}">{actorLabel(row.actor_type)}</span></td>
            <td class="details-cell">
              {#if detailsText(row.details)}
                <span class="details-compact" title={detailsText(row.details)}>
                  {detailsText(row.details)}
                </span>
              {:else}
                —
              {/if}
            </td>
          </tr>
          {#if expandedId === row.id}
            {@const detail = expandedDetail(row.id)}
            <tr class="expand-row">
              <td colspan="6">
                {#if !detail}
                  <div class="empty">Загрузка записи…</div>
                {:else}
                  <div class="expand-grid">
                    <div>
                      <div class="expand-title">
                        Было
                        {#if stateText(detail.old_state) !== null}
                          <button class="btn btn-ghost" onclick={() => copyState(row.id, 'old_state')}>
                            Скопировать
                          </button>
                        {/if}
                      </div>
                      {#if stateText(detail.old_state) !== null}
                        <pre>{stateText(detail.old_state)}</pre>
                      {:else}
                        <p class="state-note">{stateMeaning(detail.event)}</p>
                      {/if}
                    </div>
                    <div>
                      <div class="expand-title">
                        Стало
                        {#if stateText(detail.new_state) !== null}
                          <button class="btn btn-ghost" onclick={() => copyState(row.id, 'new_state')}>
                            Скопировать
                          </button>
                        {/if}
                      </div>
                      {#if stateText(detail.new_state) !== null}
                        <pre>{stateText(detail.new_state)}</pre>
                      {:else}
                        <p class="state-note">{stateMeaning(detail.event)}</p>
                      {/if}
                    </div>
                    <div>
                      <div class="expand-title">
                        Детали
                        {#if detailsText(detail.details)}
                          <button class="btn btn-ghost" onclick={() => copyState(row.id, 'details')}>
                            Скопировать
                          </button>
                        {/if}
                      </div>
                      {#if detailsText(detail.details)}
                        <pre>{stateText(detail.details)}</pre>
                      {:else}
                        <p class="state-note">Нет</p>
                      {/if}
                    </div>
                  </div>
                  <p class="expand-meta">
                    Исполнитель: <span class="mono">{detail.actor_id ?? '—'}</span>
                    · Сущность: <span class="mono">{detail.entity_id ?? '—'}</span>
                  </p>
                {/if}
              </td>
            </tr>
          {/if}
        {/each}
      </tbody>
    </table>

    <div class="pagination">
      <span>Найдено: {total}</span>
      <span class="pagination-info">
        {offset + 1}–{offset + items.length} из {total}
      </span>
      <button
        class="btn btn-ghost"
        disabled={offset === 0}
        onclick={() => {
          offset = Math.max(0, offset - limit)
          load()
        }}
      >
        ‹
      </button>
      <button
        class="btn btn-ghost"
        disabled={offset + items.length >= total}
        onclick={() => {
          offset = offset + limit
          load()
        }}
      >
        ›
      </button>
      <select bind:value={limit} onchange={applyFilters} aria-label="Строк на странице">
        <option value={50}>50</option>
        <option value={100}>100</option>
        <option value={200}>200</option>
      </select>
    </div>
  {/if}
</div>

<style>
  .badge-ro {
    align-self: center;
    padding: 2px 10px;
    border: 1px solid var(--border, #ccc);
    border-radius: 999px;
    font-size: 12px;
    color: var(--muted, #777);
    white-space: nowrap;
  }

  .toolbar {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
    align-items: center;
  }

  .toolbar select,
  .toolbar input {
    max-width: 200px;
  }

  .row {
    cursor: pointer;
  }

  .row.expanded {
    background: var(--surface-alt, rgba(0, 0, 0, 0.03));
  }

  .nowrap {
    white-space: nowrap;
  }

  .mono {
    font-family: var(--mono, ui-monospace, monospace);
    font-size: 0.9em;
  }

  .actor-badge {
    display: inline-block;
    padding: 1px 8px;
    border-radius: 999px;
    font-size: 12px;
    border: 1px solid var(--border, #ccc);
    white-space: nowrap;
  }

  .actor-admin {
    border-color: #b58a2b;
    color: #8a6a1f;
  }

  .actor-payment_provider {
    border-color: #3a7bd5;
    color: #2c5fa8;
  }

  .actor-system,
  .actor-worker {
    color: var(--muted, #777);
  }

  .details-cell {
    max-width: 260px;
  }

  .details-compact {
    display: inline-block;
    max-width: 100%;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    vertical-align: bottom;
    font-family: var(--mono, ui-monospace, monospace);
    font-size: 0.85em;
  }

  .expand-row > td {
    background: var(--surface-alt, rgba(0, 0, 0, 0.03));
  }

  .expand-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(240px, 1fr));
    gap: 12px;
  }

  .expand-title {
    display: flex;
    justify-content: space-between;
    align-items: center;
    font-weight: 600;
    margin-bottom: 4px;
  }

  .expand-grid pre {
    max-height: 240px;
    overflow: auto;
    margin: 0;
    padding: 8px;
    border: 1px solid var(--border, #ccc);
    border-radius: 6px;
    background: var(--surface, #fff);
    font-size: 12px;
  }

  .state-note {
    color: var(--muted, #777);
    font-style: italic;
    margin: 0;
  }

  .expand-meta {
    margin: 10px 0 0;
    color: var(--muted, #777);
    font-size: 13px;
  }

  .pagination {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 10px 0;
  }

  .pagination-info {
    color: var(--muted, #777);
  }
</style>
