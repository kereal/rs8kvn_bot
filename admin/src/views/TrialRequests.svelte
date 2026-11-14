<script>
  import { onMount } from 'svelte'
  import { api } from '../lib/api.js'
  import { formatDate, humanError } from '../lib/format.js'

  // Типы появятся в types.js у интегратора (typedef TrialRequestsResponse);
  // локальная форма — чтобы svelte-check не падал до объединения веток.
  /**
   * @typedef {{ id: number, ip: string, created_at: string }} TrialRequestItem
   */
  /** @type {TrialRequestItem[]} */
  let items = $state([])
  let total = $state(0)
  let loading = $state(true)
  let error = $state('')

  // Агрегат грузится отдельно и молча: сбой статистики не гасит список.
  let stats = $state(null)
  let statsError = $state(false)

  let limit = $state(50)
  let offset = $state(0)

  async function load() {
    loading = true
    error = ''
    try {
      const query = new URLSearchParams()
      query.set('limit', String(limit))
      query.set('offset', String(offset))

      const data = await api.get(`/admin/api/trials?${query.toString()}`)
      items = data.items || []
      total = data.total || 0
    } catch (err) {
      error = humanError(err)
    } finally {
      loading = false
    }
  }

  async function loadStats() {
    statsError = false
    try {
      stats = await api.get('/admin/api/trials/stats')
    } catch {
      // Карточка покажет «—», остальное остаётся живым.
      stats = null
      statsError = true
    }
  }

  onMount(() => {
    load()
    loadStats()
  })
</script>

<div class="page-head">
  <div>
    <h1>Триал-запросы</h1>
    <p class="subtitle">Антифрод: запросы триалов по IP. Экран только для чтения</p>
  </div>
  <span class="badge-ro">только чтение</span>
</div>

{#if error}
  <div class="notice notice-error">
    {error}
    <button class="btn btn-ghost" onclick={load}>Повторить</button>
  </div>
{/if}

<div class="grid">
  <section class="panel card">
    <h2>Запросов за 24 ч</h2>
    <div class="kv">
      <span>Всего</span>
      <span class="strong">{stats ? stats.requests_24h : '—'}</span>
    </div>
  </section>

  <section class="panel card">
    <h2>Уникальных IP</h2>
    <div class="kv">
      <span>За 24 ч</span>
      <span class="strong">{stats ? stats.unique_ips_24h : '—'}</span>
    </div>
  </section>

  <section class="panel card">
    <h2>Топ IP</h2>
    {#if statsError || !stats}
      <div class="kv"><span>—</span><span class="muted">не удалось загрузить</span></div>
    {:else if stats.top_ips.length === 0}
      <div class="kv"><span>—</span><span class="muted">Запросов за 24 ч нет</span></div>
    {:else}
      {#each stats.top_ips as t (t.ip)}
        <div class="kv">
          <span class="mono">{t.ip}</span>
          <span class="strong">{t.count}</span>
        </div>
      {/each}
    {/if}
  </section>
</div>

<div class="panel">
  {#if loading}
    <div class="empty">Загрузка…</div>
  {:else if items.length === 0}
    <div class="empty">Записей не найдено</div>
  {:else}
    <table>
      <thead>
        <tr>
          <th>IP</th>
          <th>Время</th>
        </tr>
      </thead>
      <tbody>
        {#each items as row (row.id)}
          <tr>
            <td class="mono">{row.ip}</td>
            <td class="nowrap">{formatDate(row.created_at)}</td>
          </tr>
        {/each}
      </tbody>
    </table>

    <div class="pager">
      <span class="muted">{offset + 1}–{offset + items.length} из {total}</span>
      <button
        class="btn btn-ghost"
        disabled={offset === 0 || loading}
        onclick={() => {
          offset = Math.max(0, offset - limit)
          load()
        }}
      >
        ‹
      </button>
      <button
        class="btn btn-ghost"
        disabled={offset + items.length >= total || loading}
        onclick={() => {
          offset = offset + limit
          load()
        }}
      >
        ›
      </button>
      <select bind:value={limit} onchange={() => { offset = 0; load() }} aria-label="Строк на странице">
        <option value={50}>50</option>
        <option value={100}>100</option>
        <option value={200}>200</option>
      </select>
    </div>
  {/if}
</div>

<style>
  .badge-ro {
    align-self: center;
    padding: 2px 10px;
    border: 1px solid var(--border);
    border-radius: 999px;
    font-size: 12px;
    color: var(--muted);
    white-space: nowrap;
  }

  /* KPI-карточки — та же сетка, что на экране «Состояние». */
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(280px, 1fr));
    gap: 16px;
    align-items: start;
  }

  .card {
    padding: 14px 16px;
  }

  .card h2 {
    margin: 0 0 12px;
    font-size: 13px;
    font-weight: 600;
    color: var(--muted);
    text-transform: uppercase;
    letter-spacing: 0.04em;
  }

  .kv {
    display: flex;
    justify-content: space-between;
    gap: 12px;
    padding: 4px 0;
  }

  .kv > span:first-child {
    color: var(--muted);
  }

  .strong {
    font-weight: 600;
  }

  .nowrap {
    white-space: nowrap;
  }
</style>
