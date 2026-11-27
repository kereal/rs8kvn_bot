<script>
  let query = $state('')
  let ambiguous = $state(null)

  const UUID_RE = /^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$/

  /** Разбор ввода: чистый классификатор, без побочных эффектов. */
  function parse(raw) {
    let value = raw.trim()
    // Явные префиксы: order:42 / #order/42 — заказ по точному id,
    // sub:42 / #sub/42 — карточка подписки по точному id.
    let match = value.match(/^(?:order:|#order\/)(\d+)$/i)
    if (match) return { kind: 'orders', q: match[1], id: match[1] }
    match = value.match(/^(?:sub:|#sub\/)(\d+)$/i)
    if (match) return { kind: 'subscription-detail', q: match[1] }
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
  // читаем в onMount целевой вью (ключ 'admin_search'). preset — опциональные
  // допполя предустановки (например { id } для фильтра точного id в заказах).
  function navigateTo(screen, q, preset = {}) {
    sessionStorage.setItem('admin_search', JSON.stringify({ screen, q, ...preset }))
    // Переход на текущий экран не меняет хэш — без события вью не
    // перемонтируется и пресет никто не прочитает. App.svelte по событию
    // поднимает nonce в ключе {#key} и перемонтирует вью принудительно.
    const sameHash = location.hash === `#/${screen}`
    location.hash = `#/${screen}`
    if (sameHash) window.dispatchEvent(new CustomEvent('admin-search'))
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
    // sub:42 — сразу карточка подписки, списка по id нет.
    if (target.kind === 'subscription-detail') {
      location.hash = `#/subscriptions/${target.q}`
      return
    }
    navigateTo(target.kind, target.q, target.id ? { id: target.id } : {})
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
      placeholder="Поиск: order:42, sub:42, telegram_id, UUID платежа…"
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
    /* .global-search несёт глобальный .toolbar (display:flex): форма —
       flex-элемент и без flex:1 сжимается до ширины инпута. */
    flex: 1;
    display: flex;
    gap: 10px;
    align-items: center;
  }

  /* Глобальный поиск растягивается на всю ширину контентной колонки:
     глобальный .search ограничен 320px для списков — здесь перекрываем. */
  .search-row .search {
    flex: 1;
    max-width: none;
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