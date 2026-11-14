<script>
  import { onMount } from 'svelte'
  import { api } from '../lib/api.js'
  import { formatDate, formatMoney } from '../lib/format.js'

  /** @type {import('../lib/types.js').Overview|null} */
  let data = $state(null)
  let loading = $state(true)
  let error = $state('')

  /** @type {import('../lib/types.js').RevenueResponse|null} */
  let revenue = $state(null)
  let revenueDays = $state(30)
  // Выручка грузится отдельно и молча: сбой агрегата не должен гасить весь
  // экран «Состояние» — карточка показывает «—», остальное остаётся живым.
  let revenueError = $state(false)

  async function load() {
    loading = true
    error = ''
    try {
      data = await api.get('/admin/api/overview')
    } catch (err) {
      error = err.message
    } finally {
      loading = false
    }
    loadRevenue()
  }

  async function loadRevenue() {
    revenueError = false
    try {
      revenue = await api.get(`/admin/api/revenue?days=${revenueDays}`)
    } catch {
      revenue = null
      revenueError = true
    }
  }

  function changeRevenueDays() {
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
</script>

<div class="page-head">
  <h1>Состояние</h1>
  <button class="btn" onclick={load} disabled={loading}>Обновить</button>
</div>

{#if error}
  <div class="notice notice-error">
    {error}
    <button class="btn btn-ghost" onclick={load}>Повторить</button>
  </div>
{:else if !data}
  <div class="empty">Загружаем…</div>
{:else}
  <div class="grid">
    <section class="panel card">
      <h2>Сборка</h2>
      <div class="kv"><span>Версия</span><span>{data.build.version || '—'}</span></div>
      <div class="kv"><span>Коммит</span><span class="mono">{shortCommit(data.build.commit)}</span></div>
      <div class="kv"><span>Собрано</span><span>{formatDate(data.build.build_time)}</span></div>
      <div class="kv"><span>Аптайм</span><span>{formatUptime(data.build.uptime_seconds)}</span></div>
    </section>

    <section class="panel card">
      <h2>База данных</h2>
      <div class="kv"><span>Файл</span><span class="mono path">{data.database.path || '—'}</span></div>
      <div class="kv"><span>Размер</span><span>{formatSize(data.database.size_bytes)}</span></div>
    </section>

    <section class="panel card">
      <h2>
        Выручка
        <select class="rev-days" bind:value={revenueDays} onchange={changeRevenueDays} aria-label="Период выручки">
          <option value={7}>7 дней</option>
          <option value={30}>30 дней</option>
          <option value={90}>90 дней</option>
        </select>
      </h2>
      {#if revenueError}
        <div class="kv"><span>—</span><span class="muted">не удалось загрузить</span></div>
      {:else if !revenue}
        <div class="kv"><span>—</span><span class="muted">Загружаем…</span></div>
      {:else if revenue.totals.paid_orders === 0}
        <div class="kv"><span>Всего</span><span class="muted">Оплат за период нет</span></div>
        <div class="kv"><span>Отменено / истекло</span><span>{revenue.totals.canceled} / {revenue.totals.expired}</span></div>
        <div class="rev-chart" role="img" aria-label="Выручка по дням">
          {#each revenue.days as d (d.date)}
            <div class="rev-bar zero" style="height:2%" title={`${d.date}: 0 зак. — 0`}></div>
          {/each}
        </div>
      {:else}
        <div class="kv">
          <span>Всего</span>
          <span class="rev-total">
            {revenue.currency ? formatMoney(revenue.totals.revenue_cents, revenue.currency) : (revenue.totals.revenue_cents / 100).toLocaleString('ru-RU')}
            {#if !revenue.currency}<span class="muted"> (несколько валют)</span>{/if}
          </span>
        </div>
        <div class="kv"><span>Оплачено</span><span>{revenue.totals.paid_orders} · ср. чек {revenue.currency ? formatMoney(revenue.totals.avg_check_cents, revenue.currency) : (revenue.totals.avg_check_cents / 100).toLocaleString('ru-RU')}</span></div>
        <div class="kv"><span>Отменено / истекло</span><span>{revenue.totals.canceled} / {revenue.totals.expired}</span></div>
        <div class="rev-chart" role="img" aria-label="Выручка по дням">
          {#each revenue.days as d (d.date)}
            <div
              class="rev-bar"
              class:zero={d.revenue_cents === 0}
              style={`height:${revBarPct(d.revenue_cents)}%`}
              title={`${d.date}: ${d.orders} зак. — ${d.revenue_cents / 100}`}
            ></div>
          {/each}
        </div>
      {/if}
    </section>

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

    <section class="panel card queue">
      <h2>Очередь узлов</h2>
      <div class="big" class:stuck={data.bindings.pending_total > 0}>{data.bindings.pending_total}</div>
      {#if data.bindings.pending_total > 0}
        <div class="notice nav-warn">
          В очереди есть несинхронизированные привязки — самая старая в очереди: {formatAge(data.bindings.oldest_pending_at)}.
          Фоновый воркер повторяет попытки сам; кнопка «Пересинхронизировать» в карточке подписки запускает синхронизацию принудительно.
        </div>
      {:else}
        <div class="notice notice-ok">Очередь пуста — все привязки синхронизированы.</div>
      {/if}
      <div class="badges">
        {#each badges(data.bindings.by_status, BINDING_STATUS) as b (b.key)}
          <span class="badge {b.cls}">{b.label}: {b.count}</span>
        {/each}
      </div>
    </section>

    <section class="panel card">
      <h2>Заказы</h2>
      <div class="kv"><span>Всего</span><span class="strong">{data.orders.total}</span></div>
      <div class="badges">
        {#each badges(data.orders.by_status, ORDER_STATUS) as b (b.key)}
          <span class="badge {b.cls}">{b.label}: {b.count}</span>
        {/each}
      </div>
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
      <h2>Активность</h2>
      <div class="kv"><span>События аудита за 24 ч</span><span class="strong">{data.audit.events_24h}</span></div>
      <div class="kv"><span>Последний запрос /sub/:id</span><span>{formatDate(data.last_sub_request)}</span></div>
      <p class="hint">Это последний запрос к серверу подписок — по нему видно, приходит ли трафик вообще.</p>
    </section>
  </div>
{/if}

<style>
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(280px, 1fr));
    gap: 16px;
    align-items: start;
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
  }

  /* Период выручки — маленький селект прямо в заголовке карточки. */
  .rev-days {
    margin-left: auto;
    font-size: 12px;
    padding: 2px 6px;
    border: 1px solid var(--border);
    border-radius: 6px;
    background: var(--bg);
    color: inherit;
    text-transform: none;
    letter-spacing: normal;
  }

  .rev-total {
    font-size: 16px;
    font-weight: 600;
  }

  /* Мини-гистограмма выручки по дням: чистый div/flex, без зависимостей. */
  .rev-chart {
    display: flex;
    align-items: flex-end;
    gap: 2px;
    height: 56px;
    margin-top: 12px;
  }

  .rev-bar {
    flex: 1 1 0;
    min-width: 3px;
    background: var(--accent);
    border-radius: 2px 2px 0 0;
    opacity: 0.85;
  }

  .rev-bar.zero {
    background: var(--border);
    opacity: 1;
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

  .queue .big {
    font-size: 34px;
    font-weight: 700;
    line-height: 1.2;
    margin-bottom: 8px;
  }

  .queue .big.stuck {
    color: var(--warn);
  }

  .nav-warn {
    border-color: color-mix(in srgb, var(--warn) 45%, var(--border));
    background: var(--warn-soft);
    color: var(--warn);
  }

  .hint {
    margin: 8px 0 0;
    font-size: 13px;
    color: var(--muted);
  }
</style>
