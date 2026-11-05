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

  let { id } = $props()
  let row = $state(null)
  let loading = $state(true)
  let error = $state('')
  let notice = $state('')
  let opError = $state('')

  // Панель действий.
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
      const days = planDays.trim() === '' ? 0 : Number(planDays)
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

  async function resetTraffic() {
    const n = activeNodes()
    if (!confirm(`Счётчик трафика обнулится на ${n} узлах. Продолжить?`)) return

    resetBusy = true
    opError = ''
    try {
      const payload = await api.post(`/admin/api/subscriptions/${id}/reset-traffic`, {})
      notice = payload.warning
        ? `Счётчик сброшен на ${payload.reset_nodes} узлах. ${payload.warning}`
        : `Счётчик трафика сброшен на ${payload.reset_nodes} узлах`
      await load()
    } catch (err) {
      opError = humanError(err)
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
      confirmError = humanError(err)
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
    <div class="notice notice-ok">{notice}</div>
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
        <dd>{row.reminders_sent} об истечении · {row.traffic_reminders_sent} о трафике</dd>
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

  <section class="panel">
    <h2>Узлы</h2>
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
              <td class="mono">{node.status}</td>
              <td class="mono muted">{node.last_error || '—'}</td>
            </tr>
          {/each}
        </tbody>
      </table>
    {/if}
  </section>

  <section class="panel actions">
    <h2>Действия</h2>
    <div class="action-row">
      <button class="btn" onclick={openPlanModal}>Продлить / сменить план</button>
      <button class="btn" onclick={resetTraffic} disabled={resetBusy || activeNodes() === 0}>Сбросить трафик</button>
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

  <Modal bind:open={confirmOpen} title={confirmMode === 'revoke' ? 'Отозвать подписку' : 'Удалить подписку'}>
    {#if confirmMode === 'revoke'}
      <p>Доступ пользователя будет закрыт: субсервер начнёт отдавать 404, VPN-клиенты удалятся с узлов.</p>
    {:else}
      <p>Строка исчезнет безвозвратно — вместе с привязками узлов и историей в панели.</p>
    {/if}
    <label class="field">
      Последние 4 символа subscription_id ({row.subscription_id.slice(-4)})
      <input class="mono" bind:value={confirmTail} placeholder={row.subscription_id.slice(-4)} />
    </label>
    {#if confirmError}
      <div class="notice notice-error" style="margin:0">{confirmError}</div>
    {/if}
    {#snippet footer()}
      <button class="btn btn-ghost" onclick={() => (confirmOpen = false)} disabled={confirmBusy}>Отмена</button>
      <button class="btn btn-danger" onclick={submitConfirm} disabled={confirmBusy}>
        {confirmMode === 'revoke' ? 'Отозвать' : 'Удалить'}
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

  .badge-cancel {
    background: var(--danger-soft);
    color: var(--danger);
  }

  .btn-danger {
    color: var(--danger);
    border-color: color-mix(in srgb, var(--danger) 45%, var(--border));
  }
</style>
