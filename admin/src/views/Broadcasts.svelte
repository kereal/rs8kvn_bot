<script>
  import { onMount } from 'svelte'
  import { api } from '../lib/api.js'
  import { formatDate, humanError } from '../lib/format.js'
  import Modal from '../lib/Modal.svelte'

  // Словарь статусов — локальная константа вью (см. ТЗ 00-common.md).
  const STATUS = {
    scheduled: { label: 'Запланирована', cls: 'badge-warn' },
    running: { label: 'Идёт', cls: 'badge-ok' },
    completed: { label: 'Завершена', cls: 'badge-muted' },
    failed: { label: 'Провалилась', cls: 'badge-fail' },
    canceled: { label: 'Отменена', cls: 'badge-cancel' }
  }

  const TEXT_LIMIT = 4096
  const PREVIEW_DEBOUNCE = 400

  /** @type {import('../lib/types.js').Broadcast[]} */
  let items = $state([])
  let total = $state(0)
  let loading = $state(true)
  let error = $state('')
  let notice = $state('')
  let limit = $state(50)
  let offset = $state(0)

  // Форма (создание/правка черновика). ever_paid — трёхсостояние:
  // '' = не важно, 'true' = платили, 'false' = не платили.
  let formOpen = $state(false)
  let editingID = $state(null)
  let saving = $state(false)
  let formError = $state('')
  let form = $state(emptyForm())

  // Предпросмотр аудитории: обновляется с debounce 400 мс, 0 блокирует «Сохранить».
  /** @type {number|null} */
  let audience = $state(null)
  let audienceError = $state('')
  let previewTimer

  // Обязательный диалог подтверждения с точным числом получателей.
  let confirmOpen = $state(false)
  let confirmText = $state('')

  function emptyForm() {
    return {
      name: '',
      text: '',
      plan_type: '',
      subscription_status: '',
      inactive_days: '',
      ever_paid: '',
      registered_after: '',
      registered_before: '',
      schedule: 'now',
      planned_local: ''
    }
  }

  async function load() {
    loading = true
    error = ''
    try {
      const data = await api.get(`/admin/api/broadcasts?limit=${limit}&offset=${offset}`)
      items = data.items || []
      total = data.total || 0
    } catch (err) {
      error = humanError(err)
    } finally {
      loading = false
    }
  }

  onMount(load)

  function pageFirst() {
    return total === 0 ? 0 : offset + 1
  }
  function pageLast() {
    return Math.min(offset + limit, total)
  }
  function prevPage() {
    offset = Math.max(0, offset - limit)
    load()
  }
  function nextPage() {
    offset += limit
    load()
  }

  function statusMeta(row) {
    return STATUS[row.status] || { label: row.status, cls: 'badge-muted' }
  }

  function progressPercent(row) {
    if (!row.recipients_total) return 0
    return Math.min(100, Math.round((row.sent_count / row.recipients_total) * 100))
  }

  function plannedLabel(row) {
    return row.planned_at ? formatDate(row.planned_at) : 'сразу'
  }

  function canCancel(row) {
    return row.status === 'scheduled' || row.status === 'running'
  }
  function canRetry(row) {
    return row.status === 'failed'
  }
  function canDelete(row) {
    return row.status !== 'running'
  }

  // --- Фильтры формы -> тело запроса ---

  function filterFromBody() {
    const f = {}
    if (form.plan_type) f.plan_type = form.plan_type
    if (form.subscription_status) f.subscription_status = form.subscription_status
    if (form.inactive_days !== '' && form.inactive_days !== null) {
      f.inactive_days = Number(form.inactive_days)
    }
    if (form.ever_paid === 'true') f.ever_paid = true
    if (form.ever_paid === 'false') f.ever_paid = false
    if (form.registered_after) f.registered_after = new Date(form.registered_after).toISOString()
    if (form.registered_before) f.registered_before = new Date(form.registered_before).toISOString()
    return f
  }

  function audienceQuery() {
    const f = filterFromBody()
    const params = new URLSearchParams()
    if (f.plan_type) params.set('plan_type', f.plan_type)
    if (f.subscription_status) params.set('subscription_status', f.subscription_status)
    if (f.inactive_days !== undefined) params.set('inactive_days', String(f.inactive_days))
    if (f.ever_paid !== undefined) params.set('ever_paid', String(f.ever_paid))
    if (f.registered_after) params.set('registered_after', f.registered_after)
    if (f.registered_before) params.set('registered_before', f.registered_before)
    return params.toString()
  }

  // Предпросмотр аудитории — debounce 400 мс на изменение фильтров.
  $effect(() => {
    if (!formOpen) return
    void filterFromBody()
    clearTimeout(previewTimer)
    previewTimer = setTimeout(async () => {
      try {
        const data = await api.get(`/admin/api/broadcasts/audience?${audienceQuery()}`)
        audience = data.count ?? 0
        audienceError = ''
      } catch (err) {
        audience = null
        audienceError = humanError(err)
      }
    }, PREVIEW_DEBOUNCE)
    return () => clearTimeout(previewTimer)
  })

  function textRunes() {
    return [...form.text].length
  }

  function plannedISO() {
    if (form.schedule !== 'at' || !form.planned_local) return null
    return new Date(form.planned_local).toISOString()
  }

  function openCreate() {
    editingID = null
    form = emptyForm()
    audience = null
    formError = ''
    formOpen = true
  }

  function openEdit(row) {
    editingID = row.id
    const f = row.filters || {}
    form = {
      name: row.name,
      text: row.message_text ?? '',
      plan_type: f.plan_type || '',
      subscription_status: f.subscription_status || '',
      inactive_days: f.inactive_days === undefined || f.inactive_days === null ? '' : String(f.inactive_days),
      ever_paid: f.ever_paid === undefined || f.ever_paid === null ? '' : String(f.ever_paid),
      registered_after: f.registered_after ? toLocalInput(f.registered_after) : '',
      registered_before: f.registered_before ? toLocalInput(f.registered_before) : '',
      schedule: row.planned_at ? 'at' : 'now',
      planned_local: row.planned_at ? toLocalInput(row.planned_at) : ''
    }
    audience = null
    formError = ''
    formOpen = true
  }

  // RFC3339 UTC -> значение для datetime-local (локальное время).
  function toLocalInput(value) {
    const d = new Date(value)
    if (Number.isNaN(d.getTime())) return ''
    const pad = (n) => String(n).padStart(2, '0')
    return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
  }

  function validateForm() {
    const name = form.name.trim()
    if (!name || name.length > 255) return 'Название обязательно, не длиннее 255 символов'
    const runes = textRunes()
    if (!runes || runes > TEXT_LIMIT) return `Текст обязателен, не длиннее ${TEXT_LIMIT} символов`
    if (form.inactive_days !== '' && (Number(form.inactive_days) < 0 || Number.isNaN(Number(form.inactive_days)))) {
      return '«Не заходили, дней» — целое число ≥ 0'
    }
    return ''
  }

  // Сохранение всегда идёт через подтверждение с точным числом получателей:
  // без диалога запрос не отправляется (§7).
  function askSave() {
    const problem = validateForm()
    if (problem) {
      formError = problem
      return
    }
    if (audience === null) {
      formError = 'Дождитесь предпросмотра аудитории'
      return
    }
    if (audience === 0) {
      formError = 'Аудитория пуста: фильтр не выбрал ни одного получателя'
      return
    }

    formError = ''
    const when = plannedISO()
    const whenLabel = when ? ` ${formatDate(when)}` : ''
    confirmText = `Отправить ${audience} пользователям${whenLabel}?`
    confirmOpen = true
  }

  async function doSave() {
    confirmOpen = false
    saving = true
    formError = ''

    const payload = {
      name: form.name.trim(),
      message_text: form.text,
      filters: filterFromBody(),
      planned_at: plannedISO()
    }

    try {
      if (editingID) {
        await api.put(`/admin/api/broadcasts/${editingID}`, payload)
        notice = 'Черновик обновлён'
      } else {
        await api.post('/admin/api/broadcasts', payload)
        notice = plannedISO()
          ? 'Рассылка запланирована'
          : 'Рассылка создана — отправка начнётся при ближайшем тике воркера'
      }
      formOpen = false
      await load()
    } catch (err) {
      formError = describeSaveError(err)
    } finally {
      saving = false
    }
  }

  function describeSaveError(err) {
    // details приходят из контракта: {"name"|"message_text"|"filters"|"planned_at": текст}.
    if (err?.details) {
      const first = Object.values(err.details)[0]
      if (first) return String(first)
    }
    return humanError(err)
  }

  async function cancelBroadcast(row) {
    const ok = confirm(`Отменить рассылку #${row.id} «${row.name}»? Получатели, которым ещё не доставлено, не получат сообщение.`)
    if (!ok) return
    error = ''
    try {
      await api.post(`/admin/api/broadcasts/${row.id}/cancel`)
      notice = `Рассылка #${row.id} отменена`
      await load()
    } catch (err) {
      error = humanError(err)
    }
  }

  async function retryBroadcast(row) {
    const ok = confirm(`Повторить доставку ошибок рассылки #${row.id}? Сообщение уйдёт тем, кому не доставили.`)
    if (!ok) return
    error = ''
    try {
      await api.post(`/admin/api/broadcasts/${row.id}/retry`)
      notice = `Повтор рассылки #${row.id} поставлен в очередь`
      await load()
    } catch (err) {
      error = humanError(err)
    }
  }

  async function deleteBroadcast(row) {
    const ok = confirm(`Удалить рассылку #${row.id} «${row.name}» вместе с отчётом? Действие необратимо.`)
    if (!ok) return
    error = ''
    try {
      await api.del(`/admin/api/broadcasts/${row.id}`)
      notice = `Рассылка #${row.id} удалена`
      await load()
    } catch (err) {
      error = humanError(err)
    }
  }
</script>

<div class="page-head">
  <div>
    <h1>Рассылки</h1>
    <p class="subtitle">
      Подготовка и контроль массовых рассылок. Панель ставит рассылку в очередь,
      отправляет её воркер бота; текст и аудитория фиксируются при создании.
    </p>
  </div>
  <div class="head-actions">
    <button class="btn" onclick={load} disabled={loading}>Обновить</button>
    <button class="btn btn-primary" onclick={openCreate}>Новая рассылка</button>
  </div>
</div>

{#if notice}
  <div class="notice notice-ok">{notice}</div>
{/if}
{#if error}
  <div class="notice notice-error">
    {error}
    <button class="btn btn-ghost retry" onclick={load}>Повторить</button>
  </div>
{/if}

<div class="panel">
  {#if loading}
    <div class="empty">Загрузка…</div>
  {:else if items.length === 0}
    <div class="empty">
      Рассылок ещё не было
      <button class="btn btn-primary" onclick={openCreate}>Создать рассылку</button>
    </div>
  {:else}
    <table>
      <thead>
        <tr>
          <th>ID</th>
          <th>НАЗВАНИЕ</th>
          <th>СТАТУС</th>
          <th>ПРОГРЕСС</th>
          <th>ПЛАН</th>
          <th>НАЧАЛО</th>
          <th class="num">ОШИБКИ</th>
          <th class="actions">ДЕЙСТВИЯ</th>
        </tr>
      </thead>
      <tbody>
        {#each items as row (row.id)}
          <tr>
            <td class="mono muted">{row.id}</td>
            <td>
              <a class="row-link" href="#/broadcasts/{row.id}">{row.name}</a>
            </td>
            <td>
              <span class="badge {statusMeta(row).cls}" title={row.last_error || undefined}>
                {statusMeta(row).label}
              </span>
            </td>
            <td>
              {#if row.status === 'scheduled'}
                <span class="muted">ждёт {plannedLabel(row)}</span>
              {:else}
                <div class="progress" title={`${row.sent_count} из ${row.recipients_total}`}>
                  <div class="progress-bar" style={`width:${progressPercent(row)}%`}></div>
                </div>
                <span class="muted small">{row.sent_count} / {row.recipients_total}</span>
              {/if}
            </td>
            <td>{plannedLabel(row)}</td>
            <td>{formatDate(row.started_at)}</td>
            <td class="num" class:zero={row.failed_count === 0}>{row.failed_count}</td>
            <td class="actions">
              <a class="btn btn-ghost" href="#/broadcasts/{row.id}">Открыть</a>
              {#if canCancel(row)}
                <button class="btn btn-ghost" onclick={() => cancelBroadcast(row)}>Отменить</button>
              {/if}
              {#if canRetry(row)}
                <button class="btn btn-ghost" onclick={() => retryBroadcast(row)}>Повторить</button>
              {/if}
              {#if canDelete(row)}
                <button class="btn btn-ghost btn-danger" onclick={() => deleteBroadcast(row)}>Удалить</button>
              {/if}
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
    <div class="pager">
      <span class="muted">{pageFirst()}–{pageLast()} из {total}</span>
      <button class="btn btn-ghost" onclick={prevPage} disabled={offset === 0 || loading}>‹</button>
      <button class="btn btn-ghost" onclick={nextPage} disabled={pageLast() >= total || loading}>›</button>
    </div>
  {/if}
</div>

<Modal bind:open={formOpen} title={editingID ? `Правка черновика #${editingID}` : 'Новая рассылка'}>
  <div class="form-grid">
    <div class="field">
      <label for="bc-name">Название</label>
      <input id="bc-name" type="text" maxlength="255" bind:value={form.name} placeholder="Чёрная пятница" />
    </div>

    <fieldset class="filters">
      <legend>Фильтры аудитории</legend>
      <div class="filter-row">
        <div class="field">
          <label for="bc-plan">Тариф</label>
          <select id="bc-plan" bind:value={form.plan_type}>
            <option value="">Все тарифы</option>
            <option value="paid">Платные</option>
            <option value="free">Бесплатные</option>
          </select>
        </div>
        <div class="field">
          <label for="bc-status">Статус подписки</label>
          <select id="bc-status" bind:value={form.subscription_status}>
            <option value="">Активные</option>
            <option value="revoked">Отозванные</option>
            <option value="all">Все</option>
          </select>
        </div>
        <div class="field">
          <label for="bc-inactive">Не заходили, дней (0 — ни разу)</label>
          <input id="bc-inactive" type="number" min="0" step="1" bind:value={form.inactive_days} placeholder="не важно" />
        </div>
        <div class="field">
          <label for="bc-everpaid">Платили хотя бы раз</label>
          <select id="bc-everpaid" bind:value={form.ever_paid}>
            <option value="">Не важно</option>
            <option value="true">Да</option>
            <option value="false">Нет</option>
          </select>
        </div>
        <div class="field">
          <label for="bc-reg-after">Регистрация после</label>
          <input id="bc-reg-after" type="datetime-local" bind:value={form.registered_after} />
        </div>
        <div class="field">
          <label for="bc-reg-before">Регистрация до</label>
          <input id="bc-reg-before" type="datetime-local" bind:value={form.registered_before} />
        </div>
      </div>
      <p class="hint">
        Получатели — активные подписки с известным Telegram ID; триальные планы исключаются всегда.
      </p>
    </fieldset>

    <div class="audience" class:audience-empty={audience === 0}>
      {#if audienceError}
        <span class="audience-err">{audienceError}</span>
      {:else if audience === null}
        <span class="muted">Считаем получателей…</span>
      {:else}
        Получателей: <strong>{audience}</strong>
        {#if audience === 0}
          <span class="audience-err">— фильтр не выбрал никого, сохранить нельзя</span>
        {/if}
      {/if}
    </div>

    <div class="field">
      <label for="bc-text">Текст сообщения</label>
      <textarea
        id="bc-text"
        rows="6"
        bind:value={form.text}
        placeholder="Текст рассылки (MarkdownV2)"></textarea>
      <span class="hint">{textRunes()} / {TEXT_LIMIT} — длинный текст уйдёт несколькими сообщениями</span>
    </div>

    <fieldset class="filters">
      <legend>Расписание</legend>
      <label class="checkbox">
        <input type="radio" bind:group={form.schedule} value="now" />
        Сразу после сохранения
      </label>
      <label class="checkbox">
        <input type="radio" bind:group={form.schedule} value="at" />
        В указанное время
      </label>
      {#if form.schedule === 'at'}
        <input class="datetime" type="datetime-local" bind:value={form.planned_local} />
        <span class="hint">время локальное, отправляется в UTC</span>
      {/if}
    </fieldset>

    {#if formError}
      <div class="notice notice-error" style="margin:0">{formError}</div>
    {/if}
  </div>

  {#snippet footer()}
    <button class="btn" onclick={() => (formOpen = false)} disabled={saving}>Отмена</button>
    <button
      class="btn btn-primary"
      onclick={askSave}
      disabled={saving || audience === null || audience === 0}
    >
      {saving ? 'Сохраняем…' : 'Сохранить'}
    </button>
  {/snippet}
</Modal>

<Modal bind:open={confirmOpen} title="Подтверждение отправки">
  <p class="confirm-text">{confirmText}</p>
  {#snippet footer()}
    <button class="btn" onclick={() => (confirmOpen = false)}>Нет, вернуться</button>
    <button class="btn btn-primary" onclick={doSave}>Да, отправить</button>
  {/snippet}
</Modal>

<style>
  .head-actions {
    display: flex;
    gap: 8px;
  }

  .badge-fail {
    background: var(--danger-soft);
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

  .progress {
    width: 120px;
    height: 6px;
    border-radius: 3px;
    background: color-mix(in srgb, var(--border) 60%, transparent);
    overflow: hidden;
    margin-bottom: 3px;
  }

  .progress-bar {
    height: 100%;
    background: var(--accent);
    transition: width 0.3s ease;
  }

  .small {
    font-size: 11.5px;
  }

  .num.zero {
    color: var(--muted);
  }

  td,
  th {
    white-space: nowrap;
  }

  .form-grid {
    display: grid;
    gap: 14px;
  }

  .filters {
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 10px 12px;
    display: grid;
    gap: 10px;
  }

  .filters legend {
    font-size: 12px;
    color: var(--muted);
    padding: 0 6px;
  }

  .filter-row {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
    gap: 10px;
  }

  .filters select,
  .datetime {
    padding: 8px 10px;
    border: 1px solid var(--border);
    border-radius: 8px;
    background: var(--bg);
  }

  textarea {
    width: 100%;
    padding: 8px 10px;
    border: 1px solid var(--border);
    border-radius: 8px;
    background: var(--bg);
    font: inherit;
    resize: vertical;
  }

  .audience {
    padding: 8px 12px;
    border: 1px solid var(--border);
    border-radius: 8px;
  }

  .audience-empty {
    border-color: var(--danger);
    color: var(--danger);
  }

  .audience-err {
    color: var(--danger);
  }

  .confirm-text {
    margin: 0;
  }

  .retry {
    margin-left: 12px;
  }
</style>
