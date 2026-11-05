<script>
  import { api, setToken } from '../lib/api.js'
  import { humanError } from '../lib/format.js'

  let { onauthed } = $props()

  let token = $state('')
  let error = $state('')
  let busy = $state(false)

  async function submit(event) {
    event.preventDefault()
    if (!token.trim()) {
      error = 'Введите токен'
      return
    }

    busy = true
    error = ''
    setToken(token.trim())
    try {
      await api.checkSession()
      onauthed()
    } catch (err) {
      setToken('')
      error = humanError(err)
    } finally {
      busy = false
    }
  }
</script>

<div class="login">
  <form class="login-card" onsubmit={submit}>
    <h1>Админка rs8kvn</h1>
    <p class="hint" style="margin:0">
      Введите ADMIN_TOKEN из окружения бота. Токен хранится только в этой вкладке.
    </p>
    <div class="field">
      <label for="token">Токен</label>
      <input
        id="token"
        type="password"
        autocomplete="current-password"
        bind:value={token}
        placeholder="ADMIN_TOKEN"
      />
    </div>
    {#if error}
      <div class="notice notice-error" style="margin:0">{error}</div>
    {/if}
    <button class="btn btn-primary" type="submit" disabled={busy}>
      {busy ? 'Проверяем…' : 'Войти'}
    </button>
  </form>
</div>
