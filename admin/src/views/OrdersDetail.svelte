<script>
  import { onMount } from 'svelte'
  import { api } from '../lib/api.js'
  import { formatDate, formatDays, formatMoney, humanError } from '../lib/format.js'

  // Словарь статусов дублирует список намеренно: каждая вью держит его локально.
  const STATUS = {
    pending: { label: 'Ожидает оплаты', cls: 'badge-warn' },
    paid: { label: 'Оплачен', cls: 'badge-ok' },
    expired: { label: 'Истёк', cls: 'badge-muted' },
    canceled: { label: 'Отменён', cls: 'badge-cancel' }
  }

  let { id } = $props()

  /** @type {import('../lib/types.js').OrderDetail|null} */
  let row = $state(null)
  let loading = $state(true)
  let error = $state('')

  async function load() {
    loading = true
    error = ''
    try {
      row = await api.get(`/admin/api/orders/${id}`)
    } catch (err) {
      error = humanError(err)
    } finally {
      loading = false
    }
  }

  onMount(load)

  // Возвраты приходят из журнала аудита (refunds): refund, авто-возврат дубля
  // и chargeback различаются событием, downgraded — факт понижения до free.
  const REFUND = {
    payment_refunded: { label: 'Возврат', cls: 'badge-cancel' },
    payment_auto_refunded: { label: 'Авто-возврат дубля', cls: 'badge-warn' },
    chargeback_received: { label: 'Чарджбэк', cls: 'badge-cancel' }
  }

  function statusMeta() {
    return STATUS[row.status] || { label: row.status, cls: 'badge-muted' }
  }

  function refundMeta(event) {
    return REFUND[event] || { label: event, cls: 'badge-muted' }
  }

  function feeDiffers() {
    return row.callback_amount_cents !== null && row.callback_amount_cents !== row.amount_cents
  }

  // Аудит этого заказа: журнал открывается отфильтрованным по entity_type/id
  // (см. AuditLog onMount). Редактируемых операций из audit нет — только история.
  function openAudit() {
    sessionStorage.setItem(
      'admin_search',
      JSON.stringify({ screen: 'audit', entity_type: 'order', entity_id: String(row.id) })
    )
    location.hash = '#/audit'
  }
</script>

<a class="back" href="#/orders">← К заказам</a>

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
        Заказ #{row.id}
        <span class="badge {statusMeta().cls}">{statusMeta().label}</span>
        {#if row.payment_creation_uncertain}<span class="badge badge-warn">Требует сверки</span>{/if}
      </h1>
      <p class="subtitle">Заказ доступен только для чтения: статус двигают платёжные транзакции.</p>
    </div>
  </div>

  <div class="cards">
    <section class="panel card">
      <h2>Платёж</h2>
      <dl>
        <dt>Сумма</dt>
        <dd>{formatMoney(row.amount_cents, row.currency)}</dd>
        <dt>Списано</dt>
        <dd class:fee-diff={feeDiffers()}>{formatMoney(row.callback_amount_cents, row.currency)}</dd>
        <dt>Комиссия провайдера</dt>
        <dd>{formatMoney(row.provider_fee_cents, row.currency)}</dd>
        <dt>Провайдер</dt>
        <dd>{row.payment_provider || '—'}</dd>
        <dt>ID платежа</dt>
        <dd class="mono">{row.provider_payment_id || '—'}</dd>
        <dt>Ссылка на оплату</dt>
        <dd>
          {#if row.payment_url}
            <a class="btn btn-ghost" href={row.payment_url} target="_blank" rel="noopener noreferrer">Открыть</a>
          {:else}
            —
          {/if}
        </dd>
        <dt>Ссылка действует до</dt>
        <dd>{formatDate(row.payment_expires_at)}</dd>
        <dt>История аудита</dt>
        <dd><button class="btn btn-ghost" onclick={openAudit}>Открыть</button></dd>
      </dl>
    </section>

    <section class="panel card">
      <h2>Даты</h2>
      <dl>
        <dt>Создан</dt>
        <dd>{formatDate(row.created_at)}</dd>
        <dt>Оплачен</dt>
        <dd>{formatDate(row.paid_at)}</dd>
        <dt>Активирован</dt>
        <dd>{formatDate(row.activated_at)}</dd>
        <dt>Действует до</dt>
        <dd>{formatDate(row.expires_at)}</dd>
      </dl>
    </section>

    <section class="panel card">
      <h2>Продукт</h2>
      <dl>
        <dt>Продукт</dt>
        <dd>{row.product_name}</dd>
        <dt>План</dt>
        <dd>{row.plan_name}</dd>
        <dt>Длительность</dt>
        <dd>{formatDays(row.duration_days)}</dd>
        <dt>Подписка</dt>
        <dd><a class="link" href="#/subscriptions/{row.subscription_id}">Подписка #{row.subscription_id}</a></dd>
      </dl>
    </section>

    {#if row.refunds?.length}
      <!-- Возвраты — только история: инициировать их из панели нельзя. -->
      <section class="panel card refunds">
        <h2>Возвраты</h2>
        <div class="refund-list">
          {#each row.refunds as r (r.created_at + r.event)}
            <div class="refund">
              <div class="refund-head">
                <span class="badge {refundMeta(r.event).cls}">{refundMeta(r.event).label}</span>
                <span class="refund-amount">{formatMoney(r.amount_cents, r.currency)}</span>
              </div>
              <div class="refund-meta">
                {formatDate(r.created_at)} · {r.actor_type}
                {#if r.downgraded}· подписка понижена до free{/if}
              </div>
            </div>
          {/each}
        </div>
      </section>
    {/if}

    {#if row.payment_creation_uncertain}
      <!-- Карточка ручной сверки: только чтение, без кнопок подтверждения.
           payment_url виден только здесь, не в списке. -->
      <section class="panel card uncertain">
        <h2>Ручная сверка</h2>
        <dl>
          <dt>Причина</dt>
          <dd>Исход создания платежа у провайдера неизвестен — деньги могли списаться</dd>
          <dt>ID платежа</dt>
          <dd class="mono">{row.provider_payment_id || '—'}</dd>
          <dt>Валюта</dt>
          <dd class="mono">{row.currency}</dd>
          <dt>Сумма</dt>
          <dd>{formatMoney(row.amount_cents, row.currency)}</dd>
          <dt>Ссылка на оплату</dt>
          <dd>
            {#if row.payment_url}
              <a class="btn btn-ghost" href={row.payment_url} target="_blank" rel="noopener noreferrer">Открыть</a>
            {:else}
              —
            {/if}
          </dd>
        </dl>
      </section>
    {/if}
  </div>
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
  }

  .card {
    padding: 14px 16px;
  }

  .card h2 {
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

  .fee-diff {
    color: var(--warn);
    font-weight: 600;
  }

  .badge-cancel {
    background: var(--danger-soft);
    color: var(--danger);
  }

  .card.uncertain {
    border-color: color-mix(in srgb, var(--warn) 45%, var(--border));
  }

  .link {
    color: var(--accent);
  }

  .refund-list {
    display: grid;
    gap: 10px;
  }

  .refund-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
  }

  .refund-amount {
    font-weight: 600;
  }

  .refund-meta {
    margin-top: 2px;
    font-size: 12px;
    color: var(--muted);
  }
</style>
