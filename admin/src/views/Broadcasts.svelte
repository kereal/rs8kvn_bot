<script>
  import { onMount, tick } from 'svelte'
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

  // Предпросмотр перед отправкой: точное число получателей, фильтр,
  // расписание и текст, разобранный как в Telegram. Ничего не отправляет —
  // только показывает, что увидит получатель.
  let previewOpen = $state(false)
  let previewing = $state(false)
  /** @type {import('../lib/types.js').BroadcastPreview|null} */
  let preview = $state(null)

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

  // Тело запроса формы — одинаковое для предпросмотра и для сохранения.
  function formPayload() {
    return {
      name: form.name.trim(),
      message_text: form.text,
      filters: filterFromBody(),
      planned_at: plannedISO()
    }
  }

  // Шаг 1 → шаг 2: кнопка «Просмотр» ничего не сохраняет и не отправляет —
  // сервер считает аудиторию и разбирает текст так же, как воркер.
  async function openPreview() {
    const problem = validateForm()
    if (problem) {
      formError = problem
      return
    }

    formError = ''
    previewing = true

    try {
      const data = await api.post('/admin/api/broadcasts/preview', formPayload())
      preview = data
      formOpen = false
      previewOpen = true
    } catch (err) {
      formError = describeSaveError(err)
    } finally {
      previewing = false
    }
  }

  // Возврат в форму из превью: курсор ставим в текст сообщения — после
  // просмотра правят обычно именно его.
  async function backToForm() {
    previewOpen = false
    formOpen = true
    await tick()
    document.getElementById('bc-text')?.focus()
  }

  // Шаг 2 → отправка: запрос уходит только после подтверждения в превью.
  async function doSave() {
    saving = true
    formError = ''

    try {
      if (editingID) {
        await api.put(`/admin/api/broadcasts/${editingID}`, formPayload())
        notice = 'Черновик обновлён'
      } else {
        await api.post('/admin/api/broadcasts', formPayload())
        notice = plannedISO()
          ? 'Рассылка запланирована'
          : 'Рассылка создана — отправка начнётся при ближайшем тике воркера'
      }
      previewOpen = false
      formOpen = false
      await load()
    } catch (err) {
      // Ошибка валидации или гонки с другой правкой — возвращаем в форму,
      // где видно, что именно не так.
      formError = describeSaveError(err)
      previewOpen = false
      formOpen = true
    } finally {
      saving = false
    }
  }

  // Текст кнопки подтверждения: точное число получателей в нём обязательно.
  function confirmLabel() {
    if (editingID) return 'Сохранить черновик'
    const count = preview?.audience_count ?? 0
    const word = count % 10 === 1 && count % 100 !== 11 ? 'получателю' : 'получателям'
    if (preview?.planned_at) return `Запланировать ${count} ${word}`
    return `Отправить ${count} ${word}`
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
          <span class="audience-err">— фильтр не выбрал никого, отправить нельзя</span>
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
      <span class="hint">
        Разметка Telegram: <b>*жирный*</b>, <i>_курсив_</i>, <s>~зачёркнутый~</s>,
        <code>`код`</code>, <code>[текст](ссылка)</code>. Как это увидит получатель — покажет превью.
      </span>
    </div>

    <fieldset class="filters">
      <legend>Расписание</legend>
      <label class="checkbox">
        <input type="radio" bind:group={form.schedule} value="now" />
        Сразу после подтверждения
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
    <button class="btn" onclick={() => (formOpen = false)} disabled={saving || previewing}>Отмена</button>
    <button class="btn btn-primary" onclick={openPreview} disabled={saving || previewing}>
      {previewing ? 'Готовим превью…' : 'Просмотр'}
    </button>
  {/snippet}
</Modal>

<Modal bind:open={previewOpen} title={editingID ? 'Предпросмотр изменений' : 'Предпросмотр рассылки'}>
  {#if preview}
    <div class="preview">
      <dl class="facts">
        <div class="fact">
          <dt>Получателей</dt>
          <dd class="fact-count" class:zero={preview.audience_count === 0}>{preview.audience_count}</dd>
        </div>
        <div class="fact">
          <dt>Аудитория</dt>
          <dd>{preview.filters_text}</dd>
        </div>
        <div class="fact">
          <dt>Отправка</dt>
          <dd>{preview.planned_at ? formatDate(preview.planned_at) : 'сразу после подтверждения'}</dd>
        </div>
      </dl>

      {#if preview.audience_count === 0}
        <div class="notice notice-error" style="margin:0">
          Фильтр не выбрал ни одного получателя — отправить рассылку нельзя.
        </div>
      {/if}

      <div class="bubble">
        {#each preview.message_runs as run}
          {#if run.type === 'pre'}
            <pre class="md-pre">{run.text}</pre>
          {:else if run.url}
            <a
              class="md-link"
              class:md-bold={run.bold}
              class:md-italic={run.italic}
              class:md-strike={run.strike}
              href={run.url}
              target="_blank"
              rel="noopener noreferrer">{run.text}</a
            >
          {:else}
            <span
              class:md-bold={run.bold}
              class:md-italic={run.italic}
              class:md-strike={run.strike}
              class:md-code={run.code}>{run.text}</span
            >
          {/if}
        {/each}
      </div>

      <p class="hint">
        Так сообщение увидит получатель. Telegram показывает <b>*жирный*</b>,
        <i>_курсив_</i>, <s>~зачёркнутый~</s>, <code>`код`</code> и ссылки
        <code>[текст](ссылка)</code>; остальное — обычный текст.
      </p>
    </div>
  {/if}

  {#snippet footer()}
    <div class="preview-actions">
      <button class="btn btn-ghost" onclick={() => backToForm()} disabled={saving}>Назад к форме</button>
      <button
        class="btn btn-primary send"
        onclick={doSave}
        disabled={saving || !preview || preview.audience_count === 0}
      >
        {saving ? 'Сохраняем…' : confirmLabel()}
      </button>
    </div>
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

  .preview {
    display: grid;
    gap: 14px;
  }

  /* Три действия в подвале не помещаются в 480px: переносятся, основная
     кнопка остаётся справа. */
  .preview-actions {
    display: flex;
    flex-wrap: wrap;
    gap: 10px;
    width: 100%;
    justify-content: flex-start;
  }

  .preview-actions .send {
    margin-left: auto;
  }

  .facts {
    display: grid;
    gap: 8px;
    margin: 0;
  }

  .fact {
    display: grid;
    grid-template-columns: 110px 1fr;
    gap: 10px;
    align-items: baseline;
  }

  .fact dt {
    font-size: 12.5px;
    color: var(--muted);
  }

  .fact dd {
    margin: 0;
  }

  .fact-count {
    font-size: 18px;
    font-weight: 600;
  }

  .fact-count.zero {
    color: var(--danger);
  }

  /* Пузырь сообщения: переносы строк сохраняются, разметка Telegram видна
     так же, как в чате. */
  .bubble {
    max-width: 460px;
    padding: 10px 12px;
    border: 1px solid var(--border);
    border-radius: 12px;
    background: var(--bg);
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }

  .md-pre {
    margin: 6px 0;
    padding: 8px 10px;
    border: 1px solid var(--border);
    border-radius: 8px;
    background: var(--panel);
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
    font-size: 12.5px;
    white-space: pre;
    overflow-x: auto;
  }

  .md-link {
    color: var(--accent);
  }

  .md-bold {
    font-weight: 600;
  }

  .md-italic {
    font-style: italic;
  }

  .md-strike {
    text-decoration: line-through;
  }

  .md-code {
    padding: 0 3px;
    border-radius: 4px;
    background: color-mix(in srgb, var(--border) 45%, transparent);
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
    font-size: 12.5px;
  }

  .retry {
    margin-left: 12px;
  }
</style>
