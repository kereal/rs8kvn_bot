<script>
  import { onMount } from 'svelte'
  import { api, getToken, setToken } from './lib/api.js'
  import Login from './views/Login.svelte'
  import Plans from './views/Plans.svelte'
  import Products from './views/Products.svelte'
  import Orders from './views/Orders.svelte'
  import OrdersDetail from './views/OrdersDetail.svelte'
  import AuditLog from './views/AuditLog.svelte'
  import Invites from './views/Invites.svelte'
  import Nodes from './views/Nodes.svelte'
  import Overview from './views/Overview.svelte'
  import Subscriptions from './views/Subscriptions.svelte'
  import SubscriptionDetail from './views/SubscriptionDetail.svelte'
  import Broadcasts from './views/Broadcasts.svelte'
  import BroadcastDetail from './views/BroadcastDetail.svelte'

  // One entry per screen. Adding a screen means adding a route here and a view
  // file — no router dependency. `detail` is optional: it renders instead of
  // `component` when the hash carries an id ("#/orders/42").
  const routes = {
    overview: { label: 'Состояние', component: Overview },
    plans: { label: 'Планы', component: Plans },
    nodes: { label: 'Ноды', component: Nodes },
    products: { label: 'Продукты', component: Products },
    invites: { label: 'Инвайты', component: Invites },
    broadcasts: { label: 'Рассылки', component: Broadcasts, detail: BroadcastDetail },
    orders: { label: 'Заказы', component: Orders, detail: OrdersDetail },
    subscriptions: { label: 'Подписки', component: Subscriptions, detail: SubscriptionDetail },
    audit: { label: 'Журнал', component: AuditLog }
  }

  let authed = $state(false)
  let checking = $state(true)
  // Hash carries an optional id: "#/subscriptions/42". Detail views read it from
  // the `id` prop; list views ignore it.
  let route = $state(routeFromHash())
  let view = $derived(route.name)
  let ActiveView = $derived(resolveView(route))

  function resolveView(current) {
    const entry = routes[current.name] ?? routes.overview
    return current.id && entry.detail ? entry.detail : entry.component
  }

  function routeFromHash() {
    const [rawName = '', rawID = ''] = location.hash.replace(/^#\/?/, '').split('/')
    const name = routes[rawName] ? rawName : 'overview'
    return { name, id: rawID || null }
  }

  function go(name) {
    location.hash = `#/${name}`
  }

  $effect(() => {
    const onHash = () => (route = routeFromHash())
    window.addEventListener('hashchange', onHash)
    return () => window.removeEventListener('hashchange', onHash)
  })

  onMount(async () => {
    if (!getToken()) {
      checking = false
      return
    }
    try {
      await api.checkSession()
      authed = true
    } catch {
      setToken('')
    } finally {
      checking = false
    }
  })

  function logout() {
    setToken('')
    authed = false
  }
</script>

{#if checking}
  <div class="empty">Проверяем токен…</div>
{:else if !authed}
  <Login onauthed={() => (authed = true)} />
{:else}
  <div class="layout">
    <aside class="sidebar">
      <div class="brand">
        rs8kvn
        <small>админка</small>
      </div>
      {#each Object.entries(routes) as [name, entry] (name)}
        <button
          class="nav-item"
          aria-current={view === name ? 'page' : undefined}
          onclick={() => go(name)}
        >
          {entry.label}
        </button>
      {/each}
      <div class="sidebar-footer">
        <button class="btn btn-ghost" onclick={logout}>Выйти</button>
      </div>
    </aside>
    <main class="main">
      <!-- Key on the full route: without it a detail→detail hop ("#/orders/1"
           to "#/orders/2") reuses the same component instance and keeps the
           previous row on screen, since views load data in onMount. -->
      {#key `${route.name}/${route.id ?? ''}`}
        <ActiveView id={route.id} />
      {/key}
    </main>
  </div>
{/if}
