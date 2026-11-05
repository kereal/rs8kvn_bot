<script>
  import { onMount } from 'svelte'
  import { api } from '../lib/api.js'
  import { formatDate, humanError } from '../lib/format.js'
  import Modal from '../lib/Modal.svelte'

  // Код бота принимает коды ^[a-z0-9]{6,16}$: они подставляются в
  // t.me/{bot}?start=share_{code}. Дублируем серверную валидацию на клиенте.
  const CODE_RE = /^[a-z0-9]{6,16}$/

  /** @type {import('../lib/types.js').Invite[]} */
  let items = $state([])
  let loading = $state(true)
  let error = $state('')
  let notice = $state('')

  // Одно поле поиска: чисто число — сравнение с Telegram ID, иначе — подстрока кода.
  let query = $state('')
  let sortKey = $state('created_at')
  let sortAsc = $state(false)

  let formOpen = $state(false)
  let saving = $state(false)
  let formError = $state('')
  let form = $state(emptyForm())
  let copiedCode = $state('')

  function emptyForm() {
    return { referrer_tg_id: '', code: '' }
  }

  async function load() {
    loading = true
    error = ''
    try {
      const data = await api.get('/admin/api/invites')
      items = data.items || []
    } catch (err) {
      error = humanError(err)
    } finally {
      loading = false
    }
  }

  onMount(load)

  let rows = $derived.by(() => {
    const needle = query.trim()
    const asNumber = /^\d+$/.test(needle)
    const filtered = needle
      ? items.filter((invite) =>
          asNumber ? String(invite.referrer_tg_id).includes(needle) : invite.code.toLowerCase().includes(needle.toLowerCase())
        )
      : items

    return [...filtered].sort((a, b) => {
      if (sortKey === 'created_at') {
        const cmp = new Date(a.created_at).getTime() - new Date(b.created_at).getTime()
        return sortAsc ? cmp : -cmp
      }
      const cmp = String(a.code).localeCompare(String(b.code), 'ru')
      return sortAsc ? cmp : -cmp
    })
  })

  function sortBy(key) {
    if (sortKey === key) sortAsc = !sortAsc
    else {
      sortKey = key
      sortAsc = key !== 'created_at'
    }
  }

  function openCreate() {
    form = emptyForm()
    formError = ''
    formOpen = true
  }

  async function copyCode(code) {
    try {
      await navigator.clipboard.writeText(code)
      copiedCode = code
      setTimeout(() => {
        if (copiedCode === code) copiedCode = ''
      }, 2000)
    } catch {
      notice = 'Не удалось скопировать — скопируйте вручную'
    }
  }

  function save() {
    const tgID = String(form.referrer_tg_id).trim()
    const code = form.code.trim()

    if (!/^\d+$/.test(tgID) || Number(tgID) <= 0) {
      formError = 'Telegram ID обязателен: положительное число'
      return
    }
    if (code && !CODE_RE.test(code)) {
      formError = 'Код: 6–16 символов, строчные латинские буквы и цифры'
      return
    }

    const payload = { referrer_tg_id: Number(tgID) }
    if (code) payload.code = code

    saving = true
    formError = ''
    api
      .post('/admin/api/invites', payload)
      .then(async (item) => {
        formOpen = false
        notice = `Инвайт ${item.code} создан`
        await load()
      })
      .catch((err) => {
        formError = humanError(err)
      })
      .finally(() => {
        saving = false
      })
  }

  function deleteTitle(invite) {
    return invite.subscriptions_count > 0 ? 'Код уже использован' : 'Удалить инвайт'
  }

  async function remove(invite) {
    const ok = confirm(`Удалить инвайт ${invite.code}? Действие необратимо.`)
    if (!ok) return

    error = ''
    try {
      await api.del(`/admin/api/invites/${encodeURIComponent(invite.code)}`)
      notice = `Инвайт ${invite.code} удалён`
      await load()
    } catch (err) {
      // Guard in_use: число подписок приходит в details контракта.
      if (err.code === 'in_use') {
        const count = err.details?.subscriptions || 0
        error = count > 0 ? `Нельзя удалить: ${count} подписок по этому коду` : humanError(err)
      } else {
        error = humanError(err)
      }
    }
  }
</script>

<div class="page-head">
  <div>
    <h1>Инвайты</h1>
    <p class="subtitle">
      Реферальные коды. У одного пользователя максимум один код; использованный код удалить нельзя.
    </p>
  </div>
  <button class="btn btn-primary" onclick={openCreate}>Новый инвайт</button>
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
  <div class="toolbar">
    <input class="search" type="search" placeholder="Поиск по коду или Telegram ID" bind:value={query} />
    <span class="muted">{rows.length} из {items.length}</span>
  </div>

  {#if loading}
    <div class="empty">Загрузка…</div>
  {:else if rows.length === 0 && items.length === 0}
    <div class="empty">
      Пока нет инвайтов
      <button class="btn btn-primary" onclick={openCreate}>Создать инвайт</button>
    </div>
  {:else if rows.length === 0}
    <div class="empty">Ничего не найдено</div>
  {:else}
    <table>
      <thead>
        <tr>
          <th class="sortable" onclick={() => sortBy('code')}>Код</th>
          <th>Telegram ID</th>
          <th class="sortable" onclick={() => sortBy('created_at')}>Создан</th>
          <th class="num">Приглашено</th>
          <th class="num">Подписок</th>
          <th></th>
        </tr>
      </thead>
      <tbody>
        {#each rows as invite (invite.code)}
          <tr>
            <td>
              <span class="mono">{invite.code}</span>
              <button class="btn btn-ghost copy" title="Скопировать код" onclick={() => copyCode(invite.code)}>
                {copiedCode === invite.code ? '✓' : '⧉'}
              </button>
              {#if invite.subscriptions_count > 0}
                <span class="badge badge-warn" style="margin-left:8px">использован</span>
              {/if}
            </td>
            <td class="mono">{invite.referrer_tg_id}</td>
            <td>{formatDate(invite.created_at)}</td>
            <td class="num" class:zero={invite.referred_count === 0}>{invite.referred_count}</td>
            <td class="num" class:zero={invite.subscriptions_count === 0}>{invite.subscriptions_count}</td>
            <td class="actions">
              <button
                class="btn btn-ghost btn-danger"
                disabled={invite.subscriptions_count > 0}
                title={deleteTitle(invite)}
                onclick={() => remove(invite)}
              >
                Удалить
              </button>
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}
</div>

<Modal bind:open={formOpen} title="Новый инвайт">
  <div class="field">
    <label for="invite-tgid">Telegram ID</label>
    <input id="invite-tgid" type="number" min="1" step="1" required bind:value={form.referrer_tg_id} />
  </div>
  <div class="field">
    <label for="invite-code">Код (необязательно)</label>
    <input id="invite-code" type="text" maxlength="16" bind:value={form.code} placeholder="сгенерируется автоматически — 8 символов" />
  </div>
  {#if formError}
    <div class="notice notice-error" style="margin:0">{formError}</div>
  {/if}

  {#snippet footer()}
    <button class="btn" onclick={() => (formOpen = false)} disabled={saving}>Отмена</button>
    <button class="btn btn-primary" onclick={save} disabled={saving}>
      {saving ? 'Создаём…' : 'Создать'}
    </button>
  {/snippet}
</Modal>

<style>
  .copy {
    padding: 0 6px;
    margin-left: 6px;
  }

  td,
  th {
    white-space: nowrap;
  }

  .retry {
    margin-left: 12px;
  }
</style>
