<script>
  import { onMount } from 'svelte'
  import { api } from '../lib/api.js'
  import { formatDate, formatMoney, formatTime, humanError } from '../lib/format.js'
  import GlobalSearch from '../lib/GlobalSearch.svelte'

  /** @type {import('../lib/types.js').Overview|null} */
  let data = $state(null)
  let loading = $state(true)
  let error = $state('')
  let updatedAt = $state(null)

  /** @type {import('../lib/types.js').RevenueResponse|null} */
  let revenue = $state(null)
  let revenueDays = $state(30)
  // Валюта агрегата выручки: '' — авто-выбор сервера (RUB при наличии,
  // иначе первая из доступных), иначе конкретный код из revenue.currencies.
  let revenueCurrency = $state('')
  // Выручка грузится отдельно и молча: сбой агрегата не должен гасить весь
  // экран «Состояние» — карточка показывает «—», остальное остаётся живым.
  let revenueError = $state(false)
  // Свежесть агрегата показываем отдельно: выручка перезагружается сама
  // (период/валюта), её время не обязано совпадать со временем основного экрана.
  let revenueUpdatedAt = $state(null)

  async function load() {
    loading = true
    error = ''
    try {
      data = await api.get('/admin/api/overview')
      updatedAt = new Date().toISOString()
    } catch (err) {
      error = humanError(err)
    } finally {
      loading = false
    }
    loadRevenue()
  }

  async function loadRevenue() {
    revenueError = false
    try {
      const currencyQuery = revenueCurrency ? `&currency=${encodeURIComponent(revenueCurrency)}` : ''
      revenue = await api.get(`/admin/api/revenue?days=${revenueDays}${currencyQuery}`)
      revenueUpdatedAt = new Date().toISOString()
    } catch {
      revenue = null
      revenueError = true
    }
  }

  // Период и валюта перезагружают агрегат заново: ответ относится строго
  // к одной валюте, сервер смешивать не умеет и не должен.
  function reloadRevenue() {
    revenue = null
    loadRevenue()
  }

  // Высота столбика в процентах от максимума; нулевые дни — минимальная полоска.
  function revBarPct(cents) {
    const max = Math.max(...(revenue?.days || []).map((d) => d.revenue_cents), 1)
    return Math.max(2, Math.round((cents / max) * 100))
  }

  onMount(load)

  // Словари статусов — локальные константы вью: подписи сущностей — не забота
  // общего format.js. Незнакомый ключ показывается сырым значением из API.
  const SUB_STATUS = {
    active: { label: 'Активны', cls: 'badge-ok' },
    expired: { label: 'Истекли', cls: 'badge-muted' },
    paused: { label: 'Пауза', cls: 'badge-warn' },
    canceled: { label: 'Отменены', cls: 'badge-cancel' },
    revoked: { label: 'Отозваны', cls: 'badge-cancel' }
  }

  const BINDING_STATUS = {
    active: { label: 'Синхронизированы', cls: 'badge-ok' },
    pending_add: { label: 'Ожидают добавления', cls: 'badge-warn' },
    pending_update: { label: 'Ожидают изменения', cls: 'badge-warn' },
    pending_remove: { label: 'Ожидают удаления', cls: 'badge-cancel' }
  }

  const ORDER_STATUS = {
    pending: { label: 'Ожидает оплаты', cls: 'badge-warn' },
    paid: { label: 'Оплачен', cls: 'badge-ok' },
    expired: { label: 'Истёк', cls: 'badge-muted' },
    canceled: { label: 'Отменён', cls: 'badge-cancel' }
  }

  const BROADCAST_STATUS = {
    scheduled: { label: 'Запланирована', cls: 'badge-warn' },
    running: { label: 'Идёт', cls: 'badge-ok' },
    completed: { label: 'Завершена', cls: 'badge-muted' },
    failed: { label: 'Провалена', cls: 'badge-cancel' },
    canceled: { label: 'Отменена', cls: 'badge-cancel' }
  }

  function plural(n, forms) {
    const mod10 = n % 10
    const mod100 = n % 100
    if (mod10 === 1 && mod100 !== 11) return forms[0]
    if (mod10 >= 2 && mod10 <= 4 && (mod100 < 12 || mod100 > 14)) return forms[1]
    return forms[2]
  }

  // Аптайм и возраст очереди считаем вручную: зависимость ради трёх строк не нужна.
  function formatUptime(seconds) {
    const s = Math.max(0, Math.floor(Number(seconds) || 0))
    if (s < 60) return 'меньше минуты'
    const days = Math.floor(s / 86400)
    const hours = Math.floor((s % 86400) / 3600)
    const minutes = Math.floor((s % 3600) / 60)
    const parts = []
    if (days) parts.push(`${days} д`)
    if (hours) parts.push(`${hours} ч`)
    if (minutes) parts.push(`${minutes} мин`)
    return parts.slice(0, 2).join(' ')
  }

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

  function formatSize(bytes) {
    const b = Number(bytes) || 0
    if (b < 1024) return `${b} Б`
    if (b < 1024 ** 2) return `${(b / 1024).toFixed(1)} КиБ`
    return `${(b / 1024 ** 2).toFixed(1)} МиБ`
  }

  function shortCommit(commit) {
    const c = String(commit || '')
    return c ? c.slice(0, 7) : '—'
  }

  // Сумма в валюте выборки; конверсия minor units живёт в format.js.
  function money(cents) {
    return formatMoney(cents, revenue?.currency || 'RUB')
  }

  // Рендерим только статусы с ненулевым счётчиком: в by_status приходят лишь
  // реально существующие ключи, но и в них нулей быть не должно.
  function badges(byStatus, dict) {
    return Object.entries(byStatus || {})
      .filter(([, count]) => count > 0)
      .map(([key, count]) => ({
        key,
        count,
        label: dict[key]?.label ?? key,
        cls: dict[key]?.cls ?? 'badge'
      }))
  }

  function goTo(hash) {
    location.hash = hash
  }

  // Ключ expiring пока не объявлен в typedef Overview (types.js — зона
  // интегратора); читаем через локальную форму, пока тип не дополнен.
  function expiringOf(d) {
    const raw = /** @type {any} */ (d)?.expiring
    return {
      total: raw?.total ?? 0,
      days: raw?.days || []
    }
  }

  let expiringView = $derived(expiringOf(data))

  // Высота столбика в процентах от максимума; нулевые дни — минимальная полоска.
  function expBarPct(count) {
    const max = Math.max(...expiringView.days.map((d) => d.count), 1)
    return Math.max(2, Math.round((count / max) * 100))
  }

  // Рабочий список: предустанавливаем окно 30 дней в селекте «Подписок».
  function openExpiring() {
    sessionStorage.setItem('expiring_in', '720')
    goTo('#/subscriptions')
  }

  // Ручная сверка: заказы с неопределённым исходом создания платежа.
  function openUncertainOrders() {
    sessionStorage.setItem(
      'admin_search',
      JSON.stringify({ screen: 'orders', uncertain: 'true' })
    )
    goTo('#/orders')
  }
</script>

<div class="page-head">
  <div>
    <h1>Состояние</h1>
    <p class="subtitle">Ключевые показатели и очереди</p>
  </div>
  <button class="btn" onclick={load} disabled={loading}>Обновить</button>
  {#if updatedAt}<span class="muted refreshed">Обновлено в {formatTime(updatedAt)}</span>{/if}
</div>

{#if error}
  <div class="notice notice-error">
    {error}
    <button class="btn btn-ghost" onclick={load}>Повторить</button>
  </div>
{:else if !data}
  <div class="empty">Загружаем…</div>
{:else}
  <div class="kpis">
    <section class="panel kpi">
      <span class="kpi-label">Выручка · {revenueDays} дней{revenue?.currency ? ` · ${revenue.currency}` : ''}</span>
      {#if revenueError}
        <span class="kpi-value">—</span>
        <span class="kpi-sub">не удалось загрузить</span>
      {:else if !revenue}
        <span class="kpi-value">—</span>
        <span class="kpi-sub">Загружаем…</span>
      {:else if revenue.totals.paid_orders === 0}
        <span class="kpi-value">—</span>
        <span class="kpi-sub">Оплат за период нет</span>
      {:else}
        <span class="kpi-value">{money(revenue.totals.revenue_cents)}</span>
        <span class="kpi-sub">{revenue.totals.paid_orders} оплат · ср. чек {money(revenue.totals.avg_check_cents)}</span>
      {/if}
      <select class="period" bind:value={revenueDays} onchange={reloadRevenue} aria-label="Период выручки">
        <option value={7}>7 дней</option>
        <option value={30}>30 дней</option>
        <option value={90}>90 дней</option>
      </select>
      <select class="period" bind:value={revenueCurrency} onchange={reloadRevenue} aria-label="Валюта выручки">
        <option value="">Авто</option>
        {#each revenue?.currencies ?? [] as cur (cur)}
          <option value={cur}>{cur}</option>
        {/each}
      </select>
      {#if revenueUpdatedAt}<span class="kpi-hint">Выручка обновлена в {formatTime(revenueUpdatedAt)}</span>{/if}
    </section>

    <section class="panel kpi">
      <span class="kpi-label">Активные подписки</span>
      <span class="kpi-value">{data.subscriptions.by_status?.active ?? 0}</span>
      <span class="kpi-sub">всего {data.subscriptions.total}</span>
    </section>

    <section class="panel kpi" class:alert={data.bindings.pending_total > 0}>
      <span class="kpi-label">Очередь узлов</span>
      <span class="kpi-value">{data.bindings.pending_total}</span>
      <span class="kpi-sub">
        {#if data.bindings.pending_total > 0}
          старейшая — {formatAge(data.bindings.oldest_pending_at)}
        {:else}
          все привязки синхронизированы
        {/if}
      </span>
    </section>

    <section class="panel kpi">
      <span class="kpi-label">События аудита · 24 ч</span>
      <span class="kpi-value">{data.audit.events_24h}</span>
      <span class="kpi-sub">последний /sub — {formatDate(data.last_sub_request)}</span>
      <span class="kpi-hint">Последний запрос к субсерверу — по нему видно, идёт ли трафик вообще.</span>
    </section>
  </div>

  {#if data.bindings.pending_total > 0}
    <!-- Текст и подсказка о «Пересинхронизировать» — из прежней карточки «Очередь узлов»:
         предупреждение остаётся полноценным, а не сжатым до одной цифры. -->
    <div class="notice nav-warn queue-note">
      В очереди есть несинхронизированные привязки — самая старая: {formatAge(data.bindings.oldest_pending_at)}.
      Фоновый воркер повторяет попытки сам; кнопка «Пересинхронизировать» в карточке подписки запускает синхронизацию принудительно.
    </div>
  {/if}

  <div class="columns">
    <div class="col-charts">

    <section class="panel card">
        <h2>
          Выручка по дням{revenue?.currency ? ` · ${revenue.currency}` : ''}
          <span class="chart-sum">{revenue ? money(revenue.totals.revenue_cents) : '—'}</span>
        </h2>
        {#if revenueError}
          <div class="empty">не удалось загрузить</div>
        {:else if !revenue}
          <div class="empty">Загружаем…</div>
        {:else if revenue.totals.paid_orders === 0}
          <div class="chart tall" role="img" aria-label="Выручка по дням">
            {#each revenue.days as d (d.date)}
              <div class="bar zero" style="height:2%" title={`${d.date}: 0 зак. — 0`}></div>
            {/each}
          </div>
          <div class="chart-foot">
            <span>Оплат за период нет</span>
            <span>Отменено / истекло {revenue.totals.canceled} / {revenue.totals.expired}</span>
          </div>
        {:else}
          <div class="chart tall" role="img" aria-label="Выручка по дням">
            {#each revenue.days as d (d.date)}
              <div
                class="bar"
                class:zero={d.revenue_cents === 0}
                style={`height:${revBarPct(d.revenue_cents)}%`}
                title={`${d.date}: ${d.orders} зак. — ${money(d.revenue_cents)}`}
              ></div>
            {/each}
          </div>
          <div class="chart-foot">
            <span>Оплачено {revenue.totals.paid_orders} · ср. чек {money(revenue.totals.avg_check_cents)}</span>
            <span>Отменено / истекло {revenue.totals.canceled} / {revenue.totals.expired}</span>
          </div>
        {/if}
      </section>

    <section class="panel card">
        <h2>
          Истекают за 30 дней
          <span class="chart-sum">{expiringView.total}</span>
        </h2>
        <div class="chart mid" role="img" aria-label="Истечения по дням">
          {#each expiringView.days as d (d.date)}
            <div
              class="bar exp"
              class:zero={d.count === 0}
              style={`height:${expBarPct(d.count)}%`}
              title={`${d.date}: ${d.count}`}
            ></div>
          {/each}
        </div>
        <button class="btn btn-ghost" onclick={openExpiring}>Открыть подписки</button>
      </section>
    </div>

    <div class="col-side">
      <section class="panel card">
        <h2>Подписки</h2>
        <div class="kv"><span>Всего</span><span class="strong">{data.subscriptions.total}</span></div>
        <div class="badges">
          {#each badges(data.subscriptions.by_status, SUB_STATUS) as b (b.key)}
            <span class="badge {b.cls}">{b.label}: {b.count}</span>
          {/each}
        </div>
        <button class="btn btn-ghost" onclick={() => goTo('#/subscriptions')}>Открыть подписки</button>
      </section>

    <section class="panel card">
      <h2>Заказы</h2>
      <div class="kv"><span>Всего</span><span class="strong">{data.orders.total}</span></div>
      <div class="badges">
        {#each badges(data.orders.by_status, ORDER_STATUS) as b (b.key)}
          <span class="badge {b.cls}">{b.label}: {b.count}</span>
        {/each}
      </div>
      {#if data.orders.uncertain > 0}
        <button class="btn btn-ghost reconcile" onclick={openUncertainOrders}>
          Требуют сверки: {data.orders.uncertain}
        </button>
      {/if}
      <button class="btn btn-ghost" onclick={() => goTo('#/orders')}>Открыть заказы</button>
    </section>

    <section class="panel card">
      <h2>Рассылки</h2>
      <div class="kv"><span>Всего</span><span class="strong">{data.broadcasts.total}</span></div>
      <div class="badges">
        {#each badges(data.broadcasts.by_status, BROADCAST_STATUS) as b (b.key)}
          <span class="badge {b.cls}">{b.label}: {b.count}</span>
        {/each}
      </div>
      <button class="btn btn-ghost" onclick={() => goTo('#/broadcasts')}>Открыть рассылки</button>
    </section>

    <section class="panel card">
      <h2>Привязки</h2>
      <div class="badges">
        {#each badges(data.bindings.by_status, BINDING_STATUS) as b (b.key)}
          <span class="badge {b.cls}">{b.label}: {b.count}</span>
        {/each}
      </div>
    </section>

    <section class="panel card">
      <h2>Система</h2>
      <div class="kv"><span>Версия</span><span>{data.build.version || '—'} ({shortCommit(data.build.commit)})</span></div>
      <div class="kv"><span>Собрано</span><span>{formatDate(data.build.build_time)}</span></div>
      <div class="kv"><span>Аптайм</span><span>{formatUptime(data.build.uptime_seconds)}</span></div>
      <div class="kv"><span>База</span><span class="mono path">{data.database.path || '—'} · {formatSize(data.database.size_bytes)}</span></div>
    </section>
    </div>
  </div>
{/if}

<!-- Глобальный поиск живёт только на «Состоянии» (решение владельца):
     во всю ширину и под всеми блоками. -->
<GlobalSearch />

<style>
  /* Свежесть рядом с кнопкой обновления; кнопка прижата вправо page-head. */
  .refreshed {
    font-size: 12px;
  }

  .reconcile {
    color: var(--warn);
  }

  /* Макет: строка KPI, затем два столбца — графики слева, статусы справа.
     Ниже — «Система» в правой колонке; глобальный поиск под всем этим. */
  .kpis {
    display: grid;
    grid-template-columns: repeat(4, 1fr);
    gap: 12px;
    margin-bottom: 16px;
  }

  .kpi {
    position: relative;
    display: flex;
    flex-direction: column;
    gap: 2px;
    padding: 14px 16px;
  }

  .kpi-label {
    font-size: 12px;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--muted);
  }

  .kpi-value {
    font-size: 30px;
    font-weight: 700;
    line-height: 1.15;
    letter-spacing: -0.02em;
    font-variant-numeric: tabular-nums;
  }

  .kpi-sub {
    font-size: 12px;
    color: var(--muted);
  }

  .kpi.alert .kpi-value {
    color: var(--warn);
  }

  .kpi-hint {
    font-size: 11px;
    color: var(--muted);
    opacity: 0.85;
  }

  /* Период выручки — маленький селект в углу KPI-плитки. */
  .period {
    position: absolute;
    top: 12px;
    right: 12px;
    font-size: 12px;
    padding: 2px 6px;
    border: 1px solid var(--border);
    border-radius: 6px;
    background: var(--bg);
    color: inherit;
  }

  .queue-note {
    margin-bottom: 16px;
  }

  .columns {
    display: grid;
    grid-template-columns: 3fr 2fr;
    gap: 16px;
    align-items: start;
  }

  .col-charts,
  .col-side {
    display: grid;
    gap: 16px;
  }

  .card {
    padding: 14px 16px;
  }

  .card h2 {
    margin: 0 0 12px;
    font-size: 13px;
    font-weight: 600;
    color: var(--muted);
    text-transform: uppercase;
    letter-spacing: 0.04em;
    display: flex;
    align-items: baseline;
    justify-content: space-between;
  }

  .chart-sum {
    font-size: 14px;
    text-transform: none;
    letter-spacing: normal;
    font-weight: 600;
  }

  /* Гистограммы: общий стиль столбиков для выручки и истечений. */
  .chart {
    display: flex;
    align-items: flex-end;
    gap: 2px;
  }

  .chart.tall {
    height: 130px;
  }

  .chart.mid {
    height: 70px;
  }

  .bar {
    flex: 1 1 0;
    min-width: 3px;
    background: var(--accent);
    border-radius: 2px 2px 0 0;
    opacity: 0.85;
  }

  .bar.exp {
    background: var(--warn);
  }

  .bar.zero {
    background: var(--border);
    opacity: 1;
  }

  .chart-foot {
    display: flex;
    justify-content: space-between;
    margin-top: 8px;
    font-size: 12px;
    color: var(--muted);
  }

  .kv {
    display: flex;
    justify-content: space-between;
    gap: 12px;
    padding: 4px 0;
  }

  .kv > span:first-child {
    color: var(--muted);
  }

  .strong {
    font-weight: 600;
  }

  .path {
    word-break: break-all;
    text-align: right;
  }

  .badges {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    margin: 8px 0;
  }

  .card .btn {
    margin-top: 8px;
  }

  .nav-warn {
    border-color: color-mix(in srgb, var(--warn) 45%, var(--border));
    background: var(--warn-soft);
    color: var(--warn);
  }

  @media (max-width: 1100px) {
    .kpis {
      grid-template-columns: repeat(2, 1fr);
    }

    .columns {
      grid-template-columns: 1fr;
    }
  }
</style>
