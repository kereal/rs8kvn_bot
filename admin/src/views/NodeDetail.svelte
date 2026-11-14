<script>
  import { onMount } from 'svelte'
  import { api } from '../lib/api.js'
  import { formatDate, bytesToGib, humanError } from '../lib/format.js'

  // Локальное зеркало nodeLiveResult (admin/nodes.go). Канонический typedef
  // живёт в lib/types.js (добавляется отдельно); при появлении там заменить
  // аннотацию ниже на import('../lib/types.js').NodeLiveResponse.
  /** @typedef {{node_id: number, available: boolean, ping_latency_ms: number, ping_error?: string, clients: number, disabled: number, traffic_bytes: number, last_online: number}} NodeLiveResponse */

  let { id } = $props()

  /** @type {import('../lib/types.js').Node|null} */
  let node = $state(null)
  let loading = $state(true)
  let error = $state('')

  // Живые данные: отдельный ленивый запрос, загрузка карточки ноды не
  // блокируется. Ошибка — тихая: карточка ноды остаётся рабочей.
  /** @type {NodeLiveResponse|null} */
  let live = $state(null)
  let liveFailed = $state(false)
  let livePending = $state(false)

  // Карточка «Нода» читает существующий список нод: отдельного GET по id нет.
  async function load() {
    loading = true
    error = ''
    try {
      const data = await api.get('/admin/api/nodes')
      node = (data.items || []).find((item) => String(item.id) === String(id)) || null
      if (!node) error = 'Нода не найдена'
    } catch (err) {
      error = humanError(err)
    } finally {
      loading = false
    }
  }

  async function loadLive() {
    livePending = true
    liveFailed = false
    try {
      live = await api.get(`/admin/api/nodes/${id}/live`)
    } catch {
      // Тихая деградация: живые данные показываем как «—».
      liveFailed = true
    } finally {
      livePending = false
    }
  }

  onMount(() => {
    load()
    loadLive()
  })
</script>

<a class="back" href="#/nodes">← К нодам</a>

{#if loading}
  <div class="empty">Загрузка…</div>
{:else if error}
  <div class="notice notice-error">
    {error}
    <button class="btn btn-ghost" onclick={load}>Повторить</button>
  </div>
{:else if node}
  <div class="page-head">
    <div>
      <h1>
        Нода #{node.id} {node.name}
        {#if node.is_active}
          <span class="badge badge-ok">активна</span>
        {:else}
          <span class="badge badge-muted">выключена</span>
        {/if}
      </h1>
      <p class="subtitle mono">{node.host || '—'}</p>
    </div>
  </div>

  <div class="cards">
    <section class="panel card">
      <h2>Нода</h2>
      <dl>
        <dt>Тип</dt>
        <dd><span class="badge badge-node-type">{node.type}</span></dd>
        <dt>Хост</dt>
        <dd class="mono">{node.host || '—'}</dd>
        <dt>Статус</dt>
        <dd>
          {#if node.is_active}
            <span class="badge badge-ok">активна</span>
          {:else}
            <span class="badge badge-muted">выключена</span>
          {/if}
        </dd>
        <dt>Inbound'ы</dt>
        <dd class="mono">{(node.inbound_ids || []).join(', ') || '—'}</dd>
        <dt>Привязано планов</dt>
        <dd>{node.plans_count}</dd>
      </dl>
    </section>

    <section class="panel card">
      <h2>
        Живые данные
        <button class="btn btn-ghost" onclick={loadLive} disabled={livePending}>
          {livePending ? 'Обновляем…' : 'Обновить'}
        </button>
      </h2>
      {#if liveFailed || (!live && !liveFailed)}
        <p class="muted">—</p>
      {:else if !live.available}
        <p class="muted">Панель не ответила{live.ping_error ? `: ${live.ping_error}` : ''}</p>
      {:else}
        <dl>
          <dt>Пинг панели</dt>
          <dd>{live.ping_latency_ms} мс</dd>
          <dt>Клиенты</dt>
          <dd>{live.clients}</dd>
          <dt>Суммарный трафик</dt>
          <dd>{bytesToGib(live.traffic_bytes)} ГБ</dd>
          <dt>Отключено панелью</dt>
          <dd>{live.disabled}</dd>
          <dt>Последний онлайн</dt>
          <dd>{live.last_online ? formatDate(live.last_online) : '—'}</dd>
        </dl>
      {/if}
    </section>
  </div>
{/if}

<style>
  h2 {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
  }
</style>
