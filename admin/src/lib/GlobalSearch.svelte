<script>
  let query = $state('')
  let ambiguous = $state(null)

  const UUID_RE = /^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$/

  /** Разбор ввода: чистый классификатор, без побочных эффектов. */
  function parse(raw) {
    let value = raw.trim()
    // Плейсхолдер предлагает «#id» — решётку снимаем, дальше правила те же.
    if (/^#\d+$/.test(value)) value = value.slice(1)
    if (!value) return null
    if (/^\d{5,}$/.test(value)) return { kind: 'subscriptions', q: value }
    if (UUID_RE.test(value)) return { kind: 'orders', q: value }
    if (/^\d{1,6}$/.test(value)) return { kind: 'ambiguous', q: value }
    return { kind: 'subscriptions', q: value }
  }

  // Мост к спискам: вью держат фильтры в своём стейте (не в URL), поэтому
  // адрес перехода несёт только экран — значение кладём в sessionStorage и
  // читаем в onMount целевой вью (ключ 'admin_search').
  function navigateTo(screen, q) {
    sessionStorage.setItem('admin_search', JSON.stringify({ screen, q }))
    location.hash = `#/${screen}`
  }

  function submit(event) {
    event.preventDefault()
    const target = parse(query)
    if (!target) return
    if (target.kind === 'ambiguous') {
      ambiguous = target.q
      return
    }
    ambiguous = null
    navigateTo(target.kind, target.q)
  }

  function choose(screen) {
    const q = ambiguous
    ambiguous = null
    query = ''
    navigateTo(screen, q)
  }

  // Клик вне инпута/меня (например по таблице под ним) закрывает мини-меню.
  $effect(() => {
    if (!ambiguous) return
    const onDown = (event) => {
      if (!event.target.closest?.('.global-search')) ambiguous = null
    }
    document.addEventListener('mousedown', onDown)
    return () => document.removeEventListener('mousedown', onDown)
  })
</script>

<div class="global-search toolbar">
  <form class="search-row" onsubmit={submit}>
    <input
      class="search"
      type="text"
      placeholder="Поиск: telegram_id, #id, UUID платежа…"
      bind:value={query}
      onkeydown={(event) => {
        if (event.key === 'Escape') ambiguous = null
      }}
    />
    {#if ambiguous}
      <div class="menu panel" role="menu">
        <button type="button" role="menuitem" onclick={() => choose('subscriptions')}>
          Подписка #{ambiguous}
        </button>
        <button type="button" role="menuitem" onclick={() => choose('orders')}>
          Заказ #{ambiguous}
        </button>
      </div>
    {/if}
  </form>
</div>

<style>
  .global-search {
    position: relative;
    padding: 10px 0 0;
    border-bottom: 0;
  }

  .search-row {
    display: flex;
    gap: 10px;
    align-items: center;
  }

  .menu {
    position: absolute;
    top: 100%;
    left: 0;
    z-index: 5;
    display: flex;
    flex-direction: column;
    margin-top: 6px;
    padding: 6px;
  }

  .menu button {
    padding: 7px 12px;
    border: 0;
    border-radius: 6px;
    background: transparent;
    font: inherit;
    color: inherit;
    text-align: left;
    cursor: pointer;
  }

  .menu button:hover {
    background: color-mix(in srgb, var(--accent) 10%, transparent);
  }
</style>