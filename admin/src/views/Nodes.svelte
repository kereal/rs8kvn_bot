<script>
  import { onMount } from 'svelte'
  import { api } from '../lib/api.js'
  import { formatDate, humanError } from '../lib/format.js'
  import Modal from '../lib/Modal.svelte'

  const NODE_TYPES = [
    { value: '3x-ui', label: '3x-ui' },
    { value: 'proxman', label: 'proxman' },
    { value: 'fetch', label: 'fetch' }
  ]

  let items = $state([])
  let loading = $state(true)
  let error = $state('')
  let notice = $state('')

  let query = $state('')
  let typeFilter = $state('all')
  let onlyActive = $state(false)
  let sortKey = $state('id')
  let sortAsc = $state(true)

  let formOpen = $state(false)
  let saving = $state(false)
  let formError = $state('')
  let form = $state(emptyForm())

  // Привязки к планам: модалка «Планы ноды».
  let bindingsOpen = $state(false)
  let bindingsNode = $state(null)
  let plans = $state([])
  let bindingChecks = $state({})
  let bindingInitial = $state({})
  let bindingsSaving = $state(false)
  let bindingsError = $state('')

  // Результаты health-проверки по id ноды, живут до перезагрузки списка.
  let health = $state({})
  let healthPending = $state({})

  function emptyForm() {
    return { id: 0, name: '', type: '3x-ui', host: '', subscription_url: '', inbounds: '', api_token: '', is_active: true }
  }

  async function load() {
    loading = true
    error = ''
    try {
      const data = await api.get('/admin/api/nodes')
      items = data.items || []
      health = {}
    } catch (err) {
      error = humanError(err)
    } finally {
      loading = false
    }
  }

  onMount(load)

  let rows = $derived.by(() => {
    const needle = query.trim().toLowerCase()
    let filtered = items
    if (needle) {
      filtered = filtered.filter(
        (node) => node.name.toLowerCase().includes(needle) || (node.host || '').toLowerCase().includes(needle)
      )
    }
    if (typeFilter !== 'all') filtered = filtered.filter((node) => node.type === typeFilter)
    if (onlyActive) filtered = filtered.filter((node) => node.is_active)

    return [...filtered].sort((a, b) => {
      const left = a[sortKey]
      const right = b[sortKey]
      const cmp = typeof left === 'number' && typeof right === 'number'
        ? left - right
        : String(left ?? '').localeCompare(String(right ?? ''), 'ru')
      return sortAsc ? cmp : -cmp
    })
  })

  function sortBy(key) {
    if (sortKey === key) sortAsc = !sortAsc
    else {
      sortKey = key
      sortAsc = true
    }
  }

  function openCreate() {
    form = emptyForm()
    formError = ''
    formOpen = true
  }

  function openEdit(node) {
    form = {
      id: node.id,
      name: node.name,
      type: node.type,
      host: node.host || '',
      subscription_url: node.subscription_url,
      inbounds: (node.inbound_ids || []).join(', '),
      api_token: '',
      is_active: node.is_active
    }
    formError = ''
    formOpen = true
  }

  function parseInbounds(raw) {
    return String(raw ?? '')
      .split(',')
      .map((part) => part.trim())
      .filter(Boolean)
      .map((part) => Number(part))
  }

  async function save() {
    const payload = {
      name: form.name.trim(),
      type: form.type,
      host: form.host.trim(),
      subscription_url: form.subscription_url.trim(),
      inbound_ids: form.type === '3x-ui' ? parseInbounds(form.inbounds) : [],
      api_token: form.api_token,
      is_active: form.is_active
    }

    if (!payload.name) {
      formError = 'Название обязательно'
      return
    }

    saving = true
    formError = ''
    try {
      if (form.id) {
        await api.put(`/admin/api/nodes/${form.id}`, payload)
        notice = `Нода «${payload.name}» сохранена`
      } else {
        await api.post('/admin/api/nodes', payload)
        notice = `Нода «${payload.name}» создана`
      }
      formOpen = false
      await load()
    } catch (err) {
      formError = humanError(err)
    } finally {
      saving = false
    }
  }

  async function remove(node) {
    const ok = confirm(`Удалить ноду «${node.name}»? Действие необратимо.`)
    if (!ok) return

    error = ''
    try {
      await api.del(`/admin/api/nodes/${node.id}`)
      notice = `Нода «${node.name}» удалена`
      await load()
    } catch (err) {
      // in_use: сервер кладёт счётчики в message — показываем их админу.
      if (err?.code === 'in_use' && err.message) {
        error = `Нельзя удалить ноду «${node.name}»: ${err.message}`
      } else {
        error = humanError(err)
      }
    }
  }

  function deleteTitle(node) {
    if (node.plans_count > 0 || node.subscriptions_count > 0) {
      return 'Нода используется планами или подписками'
    }
    return 'Удалить ноду'
  }

  function canHealth(node) {
    return node.type === '3x-ui' && node.has_api_token
  }

  function healthTitle(node) {
    if (node.type !== '3x-ui') return 'Проверка доступна только для 3x-ui'
    if (!node.has_api_token) return 'Нужен заданный api_token'
    return 'Проверить доступность панели'
  }

  async function checkHealth(node) {
    healthPending = { ...healthPending, [node.id]: true }
    try {
      const result = await api.post(`/admin/api/nodes/${node.id}/health`, {})
      health = { ...health, [node.id]: result }
    } catch (err) {
      health = { ...health, [node.id]: { available: false, error: humanError(err) } }
    } finally {
      const pending = { ...healthPending }
      delete pending[node.id]
      healthPending = pending
    }
  }

  async function openBindings(node) {
    bindingsNode = node
    bindingsError = ''
    bindingsOpen = true

    try {
      const data = await api.get('/admin/api/plans')
      plans = data.items || []

      const checks = {}
      const initial = {}
      for (const plan of plans) {
        const linked = await api.get(`/admin/api/plans/${plan.id}/nodes`)
        const has = (linked.node_ids || []).includes(node.id)
        checks[plan.id] = has
        initial[plan.id] = has
      }
      bindingChecks = checks
      bindingInitial = initial
    } catch (err) {
      bindingsError = humanError(err)
    }
  }

  async function saveBindings() {
    if (!bindingsNode) return

    bindingsSaving = true
    bindingsError = ''
    try {
      for (const plan of plans) {
        if (bindingChecks[plan.id] === bindingInitial[plan.id]) continue

        // PUT — полная замена набора: читаем текущий, добавляем/убираем ноду.
        const current = await api.get(`/admin/api/plans/${plan.id}/nodes`)
        let ids = current.node_ids || []
        if (bindingChecks[plan.id]) {
          ids = [...ids, bindingsNode.id]
        } else {
          ids = ids.filter((id) => id !== bindingsNode.id)
        }
        await api.put(`/admin/api/plans/${plan.id}/nodes`, { node_ids: ids })
      }

      notice = `Привязки ноды «${bindingsNode.name}» сохранены`
      bindingsOpen = false
      await load()
    } catch (err) {
      bindingsError = humanError(err)
    } finally {
      bindingsSaving = false
    }
  }
</script>

<div class="page-head">
  <div>
    <h1>Ноды</h1>
    <p class="subtitle">
      Панели 3x-ui, proxman- и fetch-источники. Привязка к плану определяет, на каких нодах бот
      провижинирует подписки. Тип ноды после создания менять нельзя.
    </p>
  </div>
  <button class="btn btn-primary" onclick={openCreate}>Новая нода</button>
</div>

{#if notice}
  <div class="notice notice-ok">{notice}</div>
{/if}
{#if error}
  <div class="notice notice-error">{error}</div>
{/if}

<div class="panel">
  <div class="toolbar">
    <input class="search" type="search" placeholder="Поиск по имени или хосту" bind:value={query} />
    <select bind:value={typeFilter}>
      <option value="all">все типы</option>
      {#each NODE_TYPES as type (type.value)}
        <option value={type.value}>{type.label}</option>
      {/each}
    </select>
    <label class="checkbox">
      <input type="checkbox" bind:checked={onlyActive} />
      только активные
    </label>
    <span class="muted">{rows.length} из {items.length}</span>
  </div>

  {#if loading}
    <div class="empty">Загрузка…</div>
  {:else if rows.length === 0}
    <div class="empty">
      Пока нет нод
      <div style="margin-top:12px">
        <button class="btn btn-primary" onclick={openCreate}>Новая нода</button>
      </div>
    </div>
  {:else}
    <table>
      <thead>
        <tr>
          <th class="sortable" onclick={() => sortBy('id')}>ID</th>
          <th class="sortable" onclick={() => sortBy('name')}>Имя</th>
          <th>Тип</th>
          <th>Хост</th>
          <th>Статус</th>
          <th>Планы</th>
          <th class="num">Подписки</th>
          <th>Токен</th>
          <th>Действия</th>
        </tr>
      </thead>
      <tbody>
        {#each rows as node (node.id)}
          <tr>
            <td class="mono muted">{node.id}</td>
            <td>
              {node.name}
              {#if health[node.id]}
                {#if health[node.id].available}
                  <span class="badge badge-ok" style="margin-left:8px">
                    доступна · {health[node.id].latency_ms} мс
                  </span>
                {:else}
                  <span class="badge badge-warn" style="margin-left:8px" title={health[node.id].error}>
                    недоступна
                  </span>
                {/if}
              {/if}
            </td>
            <td><span class="badge badge-node-type">{node.type}</span></td>
            <td class="mono">{node.host || '—'}</td>
            <td>
              {#if node.is_active}
                <span class="badge badge-ok">активна</span>
              {:else}
                <span class="badge badge-muted">выключена</span>
              {/if}
            </td>
            <td class="num">
              <button class="btn btn-ghost" onclick={() => openBindings(node)}>
                Планы ({node.plans_count})
              </button>
            </td>
            <td class="num">
              {#if node.subscriptions_count > 0}
                {node.subscriptions_count}
              {:else}
                <span class="muted">0</span>
              {/if}
            </td>
            <td>
              {#if node.has_api_token}
                <span class="badge badge-ok">задан</span>
              {:else}
                <span class="badge badge-muted">не задан</span>
              {/if}
            </td>
            <td class="actions">
              <button
                class="btn btn-ghost"
                disabled={!canHealth(node) || healthPending[node.id]}
                title={healthTitle(node)}
                onclick={() => checkHealth(node)}
              >
                {healthPending[node.id] ? 'Проверяем…' : 'Проверить'}
              </button>
              <button class="btn btn-ghost" onclick={() => openEdit(node)}>Изменить</button>
              <button
                class="btn btn-ghost btn-danger"
                disabled={node.plans_count > 0 || node.subscriptions_count > 0}
                title={deleteTitle(node)}
                onclick={() => remove(node)}
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

<Modal bind:open={formOpen} title={form.id ? 'Изменить ноду' : 'Новая нода'}>
  <div class="field">
    <label for="node-name">Название</label>
    <input id="node-name" type="text" maxlength="100" bind:value={form.name} />
  </div>
  <div class="field">
    <label for="node-type">Тип</label>
    <select id="node-type" bind:value={form.type} disabled={!!form.id}>
      {#each NODE_TYPES as type (type.value)}
        <option value={type.value}>{type.label}</option>
      {/each}
    </select>
    {#if form.id}
      <span class="hint">Тип менять нельзя — он определяет клиента провижининга. Нужен другой тип: удалите ноду и создайте заново.</span>
    {/if}
  </div>
  <div class="field">
    <label for="node-host">Хост панели</label>
    <input id="node-host" type="text" placeholder="http://203.0.113.10:2053" bind:value={form.host} />
    {#if form.type !== 'fetch'}
      <span class="hint">Обязателен для 3x-ui и proxman, адрес панели с схемой http(s)://.</span>
    {:else}
      <span class="hint">fetch-нода читает subscription_url, хост не нужен.</span>
    {/if}
  </div>
  <div class="field">
    <label for="node-sub-url">URL подписок</label>
    <input id="node-sub-url" type="text" placeholder="https://sub.example.com/s/" bind:value={form.subscription_url} />
  </div>
  {#if form.type === '3x-ui'}
    <div class="field">
      <label for="node-inbounds">Inbound ID (через запятую)</label>
      <input id="node-inbounds" type="text" placeholder="1, 3" bind:value={form.inbounds} />
    </div>
  {/if}
  <div class="field">
    <label for="node-token">API-токен</label>
    <input
      id="node-token"
      type="password"
      autocomplete="off"
      placeholder={form.id ? 'не менять' : ''}
      bind:value={form.api_token}
    />
    {#if form.id}
      <span class="hint">Токен не показывается: пустое поле оставит прежний.</span>
    {/if}
  </div>
  <label class="checkbox">
    <input type="checkbox" bind:checked={form.is_active} />
    Нода активна
  </label>
  {#if formError}
    <div class="notice notice-error" style="margin:0">{formError}</div>
  {/if}

  {#snippet footer()}
    <button class="btn" onclick={() => (formOpen = false)} disabled={saving}>Отмена</button>
    <button class="btn btn-primary" onclick={save} disabled={saving}>
      {saving ? 'Сохраняем…' : 'Сохранить'}
    </button>
  {/snippet}
</Modal>

<Modal bind:open={bindingsOpen} title={bindingsNode ? `Планы ноды «${bindingsNode.name}»` : 'Планы ноды'}>
  {#if bindingsError}
    <div class="notice notice-error" style="margin:0">{bindingsError}</div>
  {/if}
  {#if plans.length === 0 && !bindingsError}
    <div class="empty">Планов нет — сначала создайте план на экране «Планы».</div>
  {:else}
    <div class="bindings-list">
      {#each plans as plan (plan.id)}
        <label class="checkbox">
          <input type="checkbox" bind:checked={bindingChecks[plan.id]} />
          {plan.name}
          {#if !plan.is_active}
            <span class="badge badge-muted" style="margin-left:8px">план выключен</span>
          {/if}
        </label>
      {/each}
    </div>
    <span class="hint">Привязка определяет, на каких нодах провижинируются подписки плана. Отвязка безопасна: клиенты с ноды снимет синхронизация.</span>
  {/if}
  {#snippet footer()}
    <button class="btn" onclick={() => (bindingsOpen = false)} disabled={bindingsSaving}>Отмена</button>
    <button class="btn btn-primary" onclick={saveBindings} disabled={bindingsSaving}>
      {bindingsSaving ? 'Сохраняем…' : 'Сохранить'}
    </button>
  {/snippet}
</Modal>

<style>
  .badge-node-type {
    font-family: var(--font-mono, monospace);
  }

  .toolbar select {
    min-width: 120px;
  }
</style>

