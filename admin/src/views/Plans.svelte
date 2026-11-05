<script>
  import { onMount } from 'svelte'
  import { api } from '../lib/api.js'
  import { bytesToGib, formatDate, formatGib, gibToBytes, humanError } from '../lib/format.js'
  import Modal from '../lib/Modal.svelte'

  /** @type {import('../lib/types.js').Plan[]} */
  let items = $state([])
  let loading = $state(true)
  let error = $state('')
  let notice = $state('')

  let query = $state('')
  let sortKey = $state('id')
  let sortAsc = $state(true)

  let formOpen = $state(false)
  let saving = $state(false)
  let formError = $state('')
  let form = $state(emptyForm())

  function emptyForm() {
    return { id: 0, name: '', devices_limit: 1, traffic_gb: 0, is_active: true }
  }

  function isProtected(plan) {
    return plan.name === 'free' || plan.name === 'trial'
  }

  async function load() {
    loading = true
    error = ''
    try {
      const data = await api.listPlans()
      items = data.items || []
    } catch (err) {
      error = humanError(err)
    } finally {
      loading = false
    }
  }

  onMount(load)

  let rows = $derived.by(() => {
    const needle = query.trim().toLowerCase()
    const filtered = needle
      ? items.filter((plan) => plan.name.toLowerCase().includes(needle))
      : items

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

  function openEdit(plan) {
    form = {
      id: plan.id,
      name: plan.name,
      devices_limit: plan.devices_limit,
      traffic_gb: bytesToGib(plan.traffic_limit),
      is_active: plan.is_active,
      protectedPlan: isProtected(plan)
    }
    formError = ''
    formOpen = true
  }

  async function save() {
    const payload = {
      name: form.name.trim(),
      devices_limit: Number(form.devices_limit) || 0,
      traffic_limit: gibToBytes(form.traffic_gb),
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
        await api.updatePlan(form.id, payload)
        notice = `План «${payload.name}» сохранён`
      } else {
        await api.createPlan(payload)
        notice = `План «${payload.name}» создан`
      }
      formOpen = false
      await load()
    } catch (err) {
      formError = humanError(err)
    } finally {
      saving = false
    }
  }

  async function remove(plan) {
    const ok = confirm(`Удалить план «${plan.name}»? Действие необратимо.`)
    if (!ok) return

    error = ''
    try {
      await api.deletePlan(plan.id)
      notice = `План «${plan.name}» удалён`
      await load()
    } catch (err) {
      error = humanError(err)
    }
  }

  function deleteTitle(plan) {
    if (isProtected(plan)) return 'Базовый план нельзя удалить'
    if (plan.products_count > 0 || plan.subscriptions_count > 0) {
      return 'План используется продуктами или подписками'
    }
    return 'Удалить план'
  }
</script>

<div class="page-head">
  <div>
    <h1>Планы</h1>
    <p class="subtitle">
      Лимиты действуют на выдачу подписок. Планы free и trial — базовые, их имя и активность
      завязаны на логику бота.
    </p>
  </div>
  <button class="btn btn-primary" onclick={openCreate}>Новый план</button>
</div>

{#if notice}
  <div class="notice notice-ok">{notice}</div>
{/if}
{#if error}
  <div class="notice notice-error">{error}</div>
{/if}

<div class="panel">
  <div class="toolbar">
    <input class="search" type="search" placeholder="Поиск по названию" bind:value={query} />
    <span class="muted">{rows.length} из {items.length}</span>
  </div>

  {#if loading}
    <div class="empty">Загрузка…</div>
  {:else if rows.length === 0}
    <div class="empty">Планов нет</div>
  {:else}
    <table>
      <thead>
        <tr>
          <th class="sortable" onclick={() => sortBy('id')}>ID</th>
          <th class="sortable" onclick={() => sortBy('name')}>Название</th>
          <th>Статус</th>
          <th class="sortable num" onclick={() => sortBy('traffic_limit')}>Трафик</th>
          <th class="sortable num" onclick={() => sortBy('devices_limit')}>Устройств</th>
          <th class="sortable num" onclick={() => sortBy('products_count')}>Продукты</th>
          <th class="sortable num" onclick={() => sortBy('subscriptions_count')}>Подписки</th>
          <th></th>
        </tr>
      </thead>
      <tbody>
        {#each rows as plan (plan.id)}
          <tr>
            <td class="mono muted">{plan.id}</td>
            <td>
              {plan.name}
              {#if isProtected(plan)}
                <span class="badge badge-warn" style="margin-left:8px">базовый</span>
              {/if}
            </td>
            <td>
              {#if plan.is_active}
                <span class="badge badge-ok">активен</span>
              {:else}
                <span class="badge badge-muted">выключен</span>
              {/if}
            </td>
            <td class="num">{formatGib(plan.traffic_limit)}</td>
            <td class="num">{plan.devices_limit}</td>
            <td class="num">{plan.products_count}</td>
            <td class="num">{plan.subscriptions_count}</td>
            <td class="actions">
              <button class="btn btn-ghost" onclick={() => openEdit(plan)}>Изменить</button>
              <button
                class="btn btn-ghost btn-danger"
                disabled={isProtected(plan) || plan.products_count > 0 || plan.subscriptions_count > 0}
                title={deleteTitle(plan)}
                onclick={() => remove(plan)}
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

<Modal bind:open={formOpen} title={form.id ? 'Изменить план' : 'Новый план'}>
  <div class="field">
    <label for="plan-name">Название</label>
    <input
      id="plan-name"
      type="text"
      maxlength="50"
      bind:value={form.name}
      disabled={form.protectedPlan}
    />
    {#if form.protectedPlan}
      <span class="hint">Имя базового плана менять нельзя — по нему ищет код бота.</span>
    {/if}
  </div>
  <div class="field">
    <label for="plan-traffic">Лимит трафика, ГБ (0 — безлимит)</label>
    <input id="plan-traffic" type="number" min="0" step="0.5" bind:value={form.traffic_gb} />
    <span class="hint">Хранится в байтах: {gibToBytes(form.traffic_gb)}</span>
  </div>
  <div class="field">
    <label for="plan-devices">Лимит устройств</label>
    <input id="plan-devices" type="number" min="0" step="1" bind:value={form.devices_limit} />
    <span class="hint">Поле пока не ограничивается сервером — только отображается.</span>
  </div>
  <label class="checkbox">
    <input type="checkbox" bind:checked={form.is_active} disabled={form.name === 'trial'} />
    План активен
  </label>
  {#if form.name === 'trial'}
    <span class="hint">Trial-план нельзя отключить: по нему выдаются пробные подписки.</span>
  {/if}
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
