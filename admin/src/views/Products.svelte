<script>
  import { onMount } from 'svelte'
  import { api } from '../lib/api.js'
  import { formatDays, formatMoney, humanError } from '../lib/format.js'
  import Modal from '../lib/Modal.svelte'

  let items = $state([])
  let loading = $state(true)
  let error = $state('')
  let notice = $state('')

  let query = $state('')
  let planFilter = $state('all')
  let onlyActive = $state(false)

  let formOpen = $state(false)
  let saving = $state(false)
  let formError = $state('')
  let form = $state(emptyForm())

  function emptyForm() {
    return {
      id: 0,
      plan_id: 0,
      name: '',
      duration_days: 30,
      price_rub: 0,
      currency: 'RUB',
      is_active: true
    }
  }

  async function load() {
    loading = true
    error = ''
    try {
      const data = await api.get('/admin/api/products')
      items = data.items || []
    } catch (err) {
      error = humanError(err)
    } finally {
      loading = false
    }
  }

  onMount(load)

  let plans = $derived.by(() => {
    const seen = new Map()
    for (const item of items) {
      if (!seen.has(item.plan_id)) seen.set(item.plan_id, item.plan_name)
    }
    return [...seen.entries()].map(([id, name]) => ({ id, name }))
  })

  let rows = $derived.by(() => {
    const needle = query.trim().toLowerCase()
    return items.filter((item) => {
      if (planFilter !== 'all' && String(item.plan_id) !== planFilter) return false
      if (onlyActive && !item.is_active) return false
      if (needle) {
        const name = displayName(item).toLowerCase()
        if (!name.includes(needle)) return false
      }
      return true
    })
  })

  // Пустое имя — не пустая ячейка: показываем срок («30 дней»).
  function displayName(item) {
    return item.name || formatDays(item.duration_days)
  }

  function priceLabel(item) {
    if (!item.price_cents) return 'бесплатно'
    return formatMoney(item.price_cents, item.currency)
  }

  // Russian plural forms: 1 подписка, 2 подписки, 5 подписок.
  function plural(count, one, few, many) {
    const mod10 = count % 10
    const mod100 = count % 100
    if (mod10 === 1 && mod100 !== 11) return `${count} ${one}`
    if (mod10 >= 2 && mod10 <= 4 && (mod100 < 12 || mod100 > 14)) return `${count} ${few}`
    return `${count} ${many}`
  }

  // ApiError не несёт details, поэтому счётчики приходят в message
  // ("product in use: 12 subscriptions, 31 orders").
  function inUseText(err) {
    const subs = /(\d+)\s+subscriptions?/.exec(err?.message || '')
    const orders = /(\d+)\s+orders?/.exec(err?.message || '')
    const parts = []
    if (subs) parts.push(plural(Number(subs[1]), 'подписка', 'подписки', 'подписок'))
    if (orders) parts.push(plural(Number(orders[1]), 'заказ', 'заказа', 'заказов'))
    if (parts.length === 0) return humanError(err)
    return `нельзя удалить: ${parts.join(', ')}`
  }

  function openCreate() {
    form = emptyForm()
    if (planFilter !== 'all') {
      form.plan_id = Number(planFilter)
    } else if (plans.length === 1) {
      form.plan_id = plans[0].id
    }
    formError = ''
    formOpen = true
  }

  function openEdit(item) {
    form = {
      id: item.id,
      plan_id: item.plan_id,
      name: item.name,
      duration_days: item.duration_days,
      price_rub: item.price_cents / 100,
      currency: item.currency,
      is_active: item.is_active
    }
    formError = ''
    formOpen = true
  }

  async function save() {
    if (!form.plan_id) {
      formError = 'Выберите план'
      return
    }

    const payload = {
      plan_id: form.plan_id,
      name: form.name.trim(),
      duration_days: Number(form.duration_days) || 0,
      // Ввод в рублях, в API — центы.
      price_cents: Math.round((Number(form.price_rub) || 0) * 100),
      currency: form.currency,
      is_active: form.is_active
    }

    saving = true
    formError = ''
    try {
      if (form.id) {
        await api.put(`/admin/api/products/${form.id}`, payload)
        notice = `Продукт «${payload.name || formatDays(payload.duration_days)}» сохранён`
      } else {
        await api.post('/admin/api/products', payload)
        notice = `Продукт «${payload.name || formatDays(payload.duration_days)}» создан`
      }
      formOpen = false
      await load()
    } catch (err) {
      formError = humanError(err)
    } finally {
      saving = false
    }
  }

  async function remove(item) {
    const ok = confirm(`Удалить продукт «${displayName(item)}»? Действие необратимо.`)
    if (!ok) return

    error = ''
    try {
      await api.del(`/admin/api/products/${item.id}`)
      notice = `Продукт «${displayName(item)}» удалён`
      await load()
    } catch (err) {
      error = inUseText(err)
    }
  }
</script>

<div class="page-head">
  <div>
    <h1>Продукты</h1>
    <p class="subtitle">
      Каталог продажи: план × срок × цена. Покупка создаёт заказ и привязывает подписку к плану;
      существующие подписки читают цену из заказа, правка цен их не трогает.
    </p>
  </div>
  <button class="btn btn-primary" onclick={openCreate}>Новый продукт</button>
</div>

{#if notice}
  <div class="notice notice-ok">{notice}</div>
{/if}
{#if error}
  <div class="notice notice-error">
    {error}
    <button class="btn btn-ghost" onclick={load}>Повторить</button>
  </div>
{/if}

<div class="panel">
  <div class="toolbar">
    <input class="search" type="search" placeholder="Поиск по названию" bind:value={query} />
    <select bind:value={planFilter}>
      <option value="all">Все планы</option>
      {#each plans as plan (plan.id)}
        <option value={String(plan.id)}>{plan.name}</option>
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
      {items.length === 0 ? 'Пока нет продуктов' : 'Ничего не найдено'}
      {#if items.length === 0}
        <div style="margin-top:12px">
          <button class="btn btn-primary" onclick={openCreate}>Создать продукт</button>
        </div>
      {/if}
    </div>
  {:else}
    <table>
      <thead>
        <tr>
          <th>ID</th>
          <th>Название</th>
          <th>План</th>
          <th>Срок</th>
          <th class="num">Цена</th>
          <th>Статус</th>
          <th class="num">Подписки</th>
          <th></th>
        </tr>
      </thead>
      <tbody>
        {#each rows as item (item.id)}
          <tr>
            <td class="mono muted">{item.id}</td>
            <td>{displayName(item)}</td>
            <td>{item.plan_name}</td>
            <td>{formatDays(item.duration_days)}</td>
            <td class="num">{priceLabel(item)}</td>
            <td>
              {#if item.is_active}
                <span class="badge badge-ok">в продаже</span>
              {:else}
                <span class="badge badge-muted">скрыт</span>
              {/if}
            </td>
            <td class="num">
              {#if item.subscriptions_count > 0}
                {item.subscriptions_count}
              {:else}
                <span class="muted">0</span>
              {/if}
            </td>
            <td class="actions">
              <button class="btn btn-ghost" onclick={() => openEdit(item)}>Изменить</button>
              <button
                class="btn btn-ghost btn-danger"
                title={item.subscriptions_count > 0 || item.orders_count > 0
                  ? 'Продукт используется подписками или заказами'
                  : 'Удалить продукт'}
                onclick={() => remove(item)}
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

<Modal bind:open={formOpen} title={form.id ? 'Изменить продукт' : 'Новый продукт'}>
  <div class="field">
    <label for="product-plan">План</label>
    <select id="product-plan" bind:value={form.plan_id} disabled={form.id > 0}>
      {#each plans as plan (plan.id)}
        <option value={plan.id}>{plan.name}</option>
      {/each}
    </select>
    {#if form.id > 0}
      <span class="hint">План изменить нельзя: удалите продукт и создайте заново.</span>
    {/if}
  </div>
  <div class="field">
    <label for="product-name">Название</label>
    <input id="product-name" type="text" maxlength="100" placeholder="Например, 30 дней" bind:value={form.name} />
  </div>
  <div class="field">
    <label for="product-duration">Срок (дней)</label>
    <input id="product-duration" type="number" min="1" max="3650" bind:value={form.duration_days} />
  </div>
  <div class="field">
    <label for="product-price">Цена (₽)</label>
    <input id="product-price" type="number" min="0" step="0.01" bind:value={form.price_rub} />
  </div>
  <div class="field">
    <label for="product-currency">Валюта</label>
    <input id="product-currency" type="text" maxlength="3" bind:value={form.currency} />
  </div>
  <label class="checkbox">
    <input type="checkbox" bind:checked={form.is_active} />
    в продаже
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

<style>
  .toolbar select {
    width: auto;
  }
</style>
