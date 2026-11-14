<script>
  import { onMount } from 'svelte'
  import { api } from '../lib/api.js'
  import { formatDate, formatDays, formatMoney, humanError } from '../lib/format.js'
  import Modal from '../lib/Modal.svelte'

  const STATUS = {
    active: { label: 'Активна', cls: 'badge-ok' },
    expired: { label: 'Истекла', cls: 'badge-muted' },
    paused: { label: 'Пауза', cls: 'badge-warn' },
    canceled: { label: 'Отменена', cls: 'badge-cancel' },
    revoked: { label: 'Отозвана', cls: 'badge-cancel' }
  }

  // Статусы привязок к узлам: active — единственный устоявшийся (очередь
  // пуста), остальные pending_* означают, что сверка ещё не дошла до панели.
  const NODE_STATUS = {
    active: { label: 'Активна', cls: 'badge-ok' },
    pending_add: { label: 'Добавляется', cls: 'badge-warn' },
    pending_update: { label: 'Обновляется', cls: 'badge-warn' },
    pending_remove: { label: 'Снимается', cls: 'badge-warn' }
  }

  let { id } = $props()
  /** @type {import('../lib/types.js').SubscriptionDetail|null} */
  let row = $state(null)
  let loading = $state(true)
  let error = $state('')
  let notice = $state('')
  // Плашка сообщения бывает двух цветов: успех и «прошло не везде».
  let noticeClass = $state('notice-ok')
  let opError = $state('')

  // Панель действий.
  /** @type {import('../lib/types.js').Plan[]} */
  let plans = $state([])
  let planOpen = $state(false)
  let planChoice = $state('')
  let planDays = $state('')
  let planError = $state('')
  let planBusy = $state(false)

  // Отзыв и удаление защищены вводом хвоста subscription_id.
  let confirmOpen = $state(false)
  let confirmMode = $state('') // revoke | delete
  let confirmTail = $state('')
  let confirmError = $state('')
  let confirmBusy = $state(false)

  let resetBusy = $state(false)
  let syncBusy = $state(false)

  // jsonCount считает элементы сохранённого JSON-массива (devices/ips).
  function jsonCount(raw) {
    if (!raw) return 0
    try {
      const parsed = JSON.parse(raw)
      return Array.isArray(parsed) ? parsed.length : 0
    } catch {
      return 0
    }
  }

  async function load() {
    loading = true
    error = ''
    try {
      row = await api.get(`/admin/api/subscriptions/${id}`)
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
    loadPlans()
    load()
  })

  function statusMeta() {
    return STATUS[row.status] || { label: row.status, cls: 'badge-muted' }
  }

  function activeNodes() {
    return row.nodes_active ?? 0
  }

  function nodeStatusMeta(node) {
    return NODE_STATUS[node.status] || { label: node.status, cls: 'badge-muted' }
  }

  // В очереди всё, что ещё не устоялось: панель не подтвердила привязку.
  function queuedNodes() {
    return (row.nodes || []).filter((node) => node.status !== 'active').length
  }

  // Битовые маски напоминаний: установленный бит = «отправлено». Семантика
  // битов зафиксирована в internal/database/models.go и не меняется здесь.
  const EXPIRY_REMINDERS = [
    { bit: 1 << 0, label: 'за 3 дня', offsetMs: 3 * 864e5 },
    { bit: 1 << 1, label: 'за 1 день', offsetMs: 1 * 864e5 },
    { bit: 1 << 2, label: 'за 3 часа', offsetMs: 3 * 36e5 }
  ]
  const TRAFFIC_REMINDERS = [
    { bit: 1 << 0, label: 'израсходовано 90 %' },
    { bit: 1 << 1, label: 'лимит превышен' },
    { bit: 1 << 2, label: 'сброс + включение' }
  ]

  function maskChips(mask, defs) {
    return defs.map((d) => ({ label: d.label, sent: (mask & d.bit) !== 0 }))
  }

  function expiryChips() {
    return maskChips(row.reminders_sent, EXPIRY_REMINDERS)
  }

  function trafficChips() {
    return maskChips(row.traffic_reminders_sent, TRAFFIC_REMINDERS)
  }

  // Группа «о трафике» бессмысленна на бессрочных планах: воркер не считает
  // трафик при лимите 0. Планы грузятся асинхронно, поэтому до загрузки
  // считаем группу значимой — деградация безопасна.
  function planHasTrafficLimit() {
    const plan = plans.find((p) => p.id === row.plan_id)
    return !plan || (plan.traffic_limit ?? 0) > 0
  }

  // Что предстоит: ближайшее неотправленное напоминание об истечении, чей
  // момент (истечение − порог) ещё в будущем. Для активной подписки с датой.
  function nextExpiryReminder() {
    if (row.status !== 'active' || !row.expires_at) return null
    const expires = new Date(row.expires_at).getTime()
    const now = Date.now()
    let best = null
    for (const r of EXPIRY_REMINDERS) {
      if (row.reminders_sent & r.bit) continue
      const due = expires - r.offsetMs
      if (due > now && (best === null || due < best.due)) best = { label: r.label, due }
    }
    return best
  }

  // Принудительная сверка: тот же реконсайлер, что гоняет фоновый воркер,
  // только по одному запросу оператора. 200 с warning означает, что часть
  // панелей не ответила и привязки остались в очереди.
  async function resync() {
    syncBusy = true
    opError = ''
    try {
      const payload = await api.post(`/admin/api/subscriptions/${id}/sync`, {})
      row = payload.subscription
      notice = payload.warning || 'Сверка выполнена'
      noticeClass = payload.warning ? 'notice-warn' : 'notice-ok'
    } catch (err) {
      opError = humanError(err)
    } finally {
      syncBusy = false
    }
  }

  function openPlanModal() {
    planChoice = row.plan_id ? String(row.plan_id) : ''
    planDays = ''
    planError = ''
    planOpen = true
  }

  async function submitPlan() {
    if (!planChoice) {
      planError = 'Выберите план'
      return
    }
    planBusy = true
    planError = ''
    try {
      // Пусто = 0: сервис сохранит текущий срок (или выдаст 30 дней
      // подписке без срока).
      // bind:value на input[type=number] отдаёт число (или undefined при
      // пустом поле), поэтому приводим типы до сравнения.
      const rawDays = planDays === undefined || planDays === null ? '' : String(planDays).trim()
      const days = rawDays === '' ? 0 : Number(rawDays)
      await api.post(`/admin/api/subscriptions/${id}/plan`, { plan_id: Number(planChoice), days })
      planOpen = false
      notice = 'План обновлён'
      await load()
    } catch (err) {
      planError = humanError(err)
    } finally {
      planBusy = false
    }
  }

  // Сброс трафика — тоже операция «с последствиями»: спрашиваем через тот же
  // диалог, что и отзыв, а не через window.confirm (его нельзя стилизовать и
  // он блокирует автотесты).
  function askResetTraffic() {
    confirmMode = 'reset'
    confirmTail = ''
    confirmError = ''
    confirmOpen = true
  }

  async function resetTraffic() {
    resetBusy = true
    opError = ''
    try {
      const payload = await api.post(`/admin/api/subscriptions/${id}/reset-traffic`, {})
      notice = payload.warning
        ? `Счётчик сброшен на ${payload.reset_nodes} узлах. ${payload.warning}`
        : `Счётчик трафика сброшен на ${payload.reset_nodes} узлах`
      noticeClass = payload.warning ? 'notice-warn' : 'notice-ok'
      await load()
    } catch (err) {
      // in_use здесь означает не «на запись ссылаются», а «ни один узел не
      // ответил» (details.nodes=0): общий текст только путал бы.
      opError =
        err?.code === 'in_use' && err?.details?.nodes === 0
          ? 'Сброс не выполнен: ни один узел не ответил'
          : humanError(err)
    } finally {
      resetBusy = false
    }
  }

  function openConfirm(mode) {
    confirmMode = mode
    confirmTail = ''
    confirmError = ''
    confirmOpen = true
  }

  // Хвост subscription_id должен совпасть: защита от случайного клика.
  function tailMatches() {
    const expected = (row.subscription_id || '').slice(-4)
    return expected.length >= 4 && confirmTail.trim() === expected
  }

  async function submitConfirm() {
    if (confirmMode === 'reset') {
      confirmOpen = false
      await resetTraffic()
      return
    }

    if (!tailMatches()) {
      confirmError = `Введите последние 4 символа subscription_id (${row.subscription_id.slice(-4)})`
      return
    }

    confirmBusy = true
    confirmError = ''
    try {
      if (confirmMode === 'revoke') {
        await api.post(`/admin/api/subscriptions/${id}/revoke`, {})
        notice = 'Подписка отозвана: доступ закрыт, VPN-клиенты удаляются'
      } else {
        await api.del(`/admin/api/subscriptions/${id}`)
        notice = ''
        confirmOpen = false
        location.hash = '#/subscriptions'
        return
      }
      confirmOpen = false
      await load()
    } catch (err) {
      // Отзыв уже случился, но профили на узлах ещё снимаются: строка
      // остаётся, доступ закрыт, удаление повторится после фоновой уборки.
      confirmError =
        err?.details?.reason === 'deprovision_incomplete'
          ? 'Доступ закрыт, но снятие профилей с узлов ещё идёт — повторите удаление позже'
          : humanError(err)
    } finally {
      confirmBusy = false
    }
  }

  async function copySubURL() {
    try {
      await navigator.clipboard.writeText(row.sub_url)
      notice = 'Ссылка скопирована'
    } catch {
      notice = ''
    }
  }
</script>

<a class="back" href="#/subscriptions">← К подпискам</a>

{#if loading}
  <div class="empty">Загрузка…</div>
{:else if error}
  <div class="notice notice-error">
    {error}
    <button class="btn btn-ghost" onclick={load}>Повторить</button>
  </div>
{:else}
  <div class="page-head">
    <div>
      <h1>
        Подписка #{row.id}
        <span class="badge {statusMeta().cls}">{statusMeta().label}</span>
      </h1>
      <p class="subtitle mono">{row.subscription_id}</p>
    </div>
  </div>

  {#if notice}
    <div class="notice {noticeClass}">{notice}</div>
  {/if}
  {#if opError}
    <div class="notice notice-error">{opError}</div>
  {/if}

  <div class="cards">
    <section class="panel card">
      <h2>Пользователь</h2>
      <dl>
        <dt>telegram_id</dt>
        <dd class="mono">{row.telegram_id}</dd>
        <dt>username</dt>
        <dd>{row.username ? '@' + row.username : '—'}</dd>
        <dt>Пришёл по инвайту</dt>
        <dd>{row.referred_by ?? '—'}</dd>
        <dt>Инвайт-код</dt>
        <dd class="mono">{row.invite_code || '—'}</dd>
      </dl>
    </section>

    <section class="panel card">
      <h2>Подписка</h2>
      <dl>
        <dt>План</dt>
        <dd>{row.plan_name || '—'}</dd>
        <dt>Продукт</dt>
        <dd>{row.product_id ? '#' + row.product_id : '—'}</dd>
        <dt>Длительность</dt>
        <dd>{row.duration_days != null ? formatDays(row.duration_days) : '—'}</dd>
        <dt>Оплачено</dt>
        <dd>{formatMoney(row.price_paid_cents, row.currency)}</dd>
        <dt>Создана</dt>
        <dd>{formatDate(row.created_at)}</dd>
        <dt>Начата</dt>
        <dd>{formatDate(row.started_at)}</dd>
        <dt>Истекает</dt>
        <dd>{formatDate(row.expires_at)}</dd>
        <dt>Напоминания</dt>
        <dd>
          <div class="rem-group">
            <span class="rem-title">об истечении</span>
            {#each expiryChips() as chip}
              <span
                class="rem-chip"
                class:on={chip.sent}
                title={chip.sent ? 'Отправлено воркером' : 'Ещё не отправлялось'}
              >{chip.label}</span>
            {/each}
          </div>
          {#if planHasTrafficLimit()}
            <div class="rem-group">
              <span class="rem-title">о трафике</span>
              {#each trafficChips() as chip}
                <span
                  class="rem-chip"
                  class:on={chip.sent}
                  title={chip.sent ? 'Отправлено воркером' : 'Порог ещё не достигнут — воркер отправит сам'}
                >{chip.label}</span>
              {/each}
            </div>
          {:else}
            <div class="rem-group">
              <span class="rem-title">о трафике</span>
              <span class="rem-muted">план без лимита трафика</span>
            </div>
          {/if}
          {#if nextExpiryReminder()}
            <div class="rem-next">
              Предстоит: {nextExpiryReminder().label} — {formatDate(new Date(nextExpiryReminder().due).toISOString())}
            </div>
          {/if}
        </dd>
      </dl>
    </section>

    <section class="panel card">
      <h2>Состояние</h2>
      <dl>
        <dt>Статус</dt>
        <dd><span class="badge {statusMeta().cls}">{statusMeta().label}</span></dd>
        <dt>Устройства</dt>
        <dd class="mono small">{jsonCount(row.devices)} шт.</dd>
        <dt>IP</dt>
        <dd class="mono small">{jsonCount(row.ips)} шт.</dd>
        <dt>Последний запрос</dt>
        <dd>{formatDate(row.last_request)}</dd>
        <dt>Ссылка подписки</dt>
        <dd class="mono small">
          {row.sub_url}
          <button class="btn btn-ghost" onclick={copySubURL}>Скопировать</button>
        </dd>
      </dl>
    </section>
  </div>

  <section class="panel nodes-panel">
    <h2>
      Узлы
      {#if queuedNodes() > 0}
        <span class="badge badge-warn">в очереди: {queuedNodes()}</span>
      {/if}
    </h2>
    {#if row.nodes.length === 0}
      <div class="empty">Привязок к узлам нет</div>
    {:else}
      <table>
        <thead>
          <tr>
            <th>УЗЕЛ</th>
            <th>СТАТУС</th>
            <th>ОШИБКА</th>
          </tr>
        </thead>
        <tbody>
          {#each row.nodes as node (node.node_id)}
            <tr>
              <td class="mono">#{node.node_id} {node.node_name}</td>
              <td><span class="badge {nodeStatusMeta(node).cls}">{nodeStatusMeta(node).label}</span></td>
              <td class="mono muted">{node.last_error || '—'}</td>
            </tr>
          {/each}
        </tbody>
      </table>
    {/if}
  </section>

  <section class="panel actions-panel">
    <h2>Действия</h2>
    <div class="action-row">
      <button class="btn" onclick={openPlanModal}>Продлить / сменить план</button>
      <button class="btn" onclick={askResetTraffic} disabled={resetBusy || activeNodes() === 0}>Сбросить трафик</button>
      <button class="btn" onclick={resync} disabled={syncBusy}>Пересинхронизировать</button>
      <button class="btn btn-danger" onclick={() => openConfirm('revoke')} disabled={row.status === 'revoked'}>Отозвать</button>
      <button class="btn btn-danger" onclick={() => openConfirm('delete')}>Удалить</button>
    </div>
    <p class="muted small">
      Продление и смена плана синхронизируют узлы автоматически. Отзыв закрывает
      доступ, но сохраняет строку. Удаление необратимо.
    </p>
  </section>

  <Modal bind:open={planOpen} title="Продлить / сменить план">
    <label class="field">
      План
      <select bind:value={planChoice}>
        {#each plans as plan (plan.id)}
          <option value={String(plan.id)}>{plan.name}</option>
        {/each}
      </select>
    </label>
    <label class="field">
      Дней
      <input type="number" min="0" bind:value={planDays} placeholder="пусто = сохранить срок" />
    </label>
    <p class="muted small">0 = сохранить срок, пусто = 30 дней для плана без срока.</p>
    {#if planError}
      <div class="notice notice-error" style="margin:0">{planError}</div>
    {/if}
    {#snippet footer()}
      <button class="btn btn-ghost" onclick={() => (planOpen = false)} disabled={planBusy}>Отмена</button>
      <button class="btn" onclick={submitPlan} disabled={planBusy}>Применить</button>
    {/snippet}
  </Modal>

  <Modal
    bind:open={confirmOpen}
    title={confirmMode === 'revoke'
      ? 'Отозвать подписку'
      : confirmMode === 'reset'
        ? 'Сбросить трафик'
        : 'Удалить подписку'}
  >
    {#if confirmMode === 'reset'}
      <p>
        Счётчик трафика обнулится на {activeNodes()} узлах. Отменить это нельзя: израсходованное
        не вернуть.
      </p>
    {:else if confirmMode === 'revoke'}
      <p>Доступ пользователя будет закрыт: субсервер начнёт отдавать 404, VPN-клиенты удалятся с узлов.</p>
    {:else}
      <p>
        Строка исчезнет безвозвратно: доступ закроется, профили на узлах снимутся, история
        останется в журнале аудита.
      </p>
    {/if}
    {#if confirmMode !== 'reset'}
      <label class="field">
        Последние 4 символа subscription_id ({row.subscription_id.slice(-4)})
        <input class="mono" bind:value={confirmTail} placeholder={row.subscription_id.slice(-4)} />
      </label>
    {/if}
    {#if confirmError}
      <div class="notice notice-error" style="margin:0">{confirmError}</div>
    {/if}
    {#snippet footer()}
      <button class="btn btn-ghost" onclick={() => (confirmOpen = false)} disabled={confirmBusy}>Отмена</button>
      <button class="btn btn-danger" onclick={submitConfirm} disabled={confirmBusy}>
        {confirmMode === 'revoke' ? 'Отозвать' : confirmMode === 'reset' ? 'Сбросить' : 'Удалить'}
      </button>
    {/snippet}
  </Modal>
{/if}

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

  .cards {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(280px, 1fr));
    gap: 14px;
    margin-bottom: 14px;
  }

  .card {
    padding: 14px 16px;
  }

  .card h2,
  section > h2 {
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

  section.panel {
    margin-bottom: 14px;
  }

  /* Секции без карточки: заголовок и содержимое отбиты отступом от края.
     Таблица узлов остаётся во всю ширину панели — как на других экранах,
     поэтому отступ получает заголовок, а не секция. */
  .nodes-panel > h2 {
    padding: 14px 14px 0;
  }

  .actions-panel {
    padding: 14px 16px;
  }

  .action-row {
    display: flex;
    flex-wrap: wrap;
    gap: 10px;
    margin-bottom: 10px;
  }

  .field {
    display: block;
    margin-bottom: 10px;
    color: var(--muted);
  }

  .field select,
  .field input {
    display: block;
    width: 100%;
    margin-top: 4px;
    padding: 7px 10px;
    border: 1px solid var(--border);
    border-radius: 8px;
    background: var(--bg);
    color: inherit;
  }

  .small {
    font-size: 12px;
  }

  .rem-group {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 6px;
    margin-bottom: 6px;
  }

  .rem-title {
    font-size: 12px;
    color: var(--muted);
    min-width: 88px;
  }

  .rem-chip {
    display: inline-block;
    padding: 2px 9px;
    border-radius: 12px;
    font-size: 12px;
    border: 1px solid var(--border);
    color: var(--muted);
    white-space: nowrap;
  }

  .rem-chip.on {
    background: var(--ok-soft);
    border-color: transparent;
    color: var(--ok);
  }

  .rem-muted {
    font-size: 12px;
    color: var(--muted);
    font-style: italic;
  }

  .rem-next {
    font-size: 12px;
    color: var(--warn);
  }

  .badge-cancel {
    background: var(--danger-soft);
    color: var(--danger);
  }

  .btn-danger {
    color: var(--danger);
    border-color: color-mix(in srgb, var(--danger) 45%, var(--border));
  }
</style>
