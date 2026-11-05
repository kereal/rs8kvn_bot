<script>
  let { open = $bindable(false), title = '', children, footer } = $props()

  let dialog = $state(null)

  $effect(() => {
    if (!dialog) return
    if (open && !dialog.open) dialog.showModal()
    if (!open && dialog.open) dialog.close()
  })

  function handleCancel(event) {
    event.preventDefault()
    open = false
  }

  function handleClick(event) {
    if (event.target === dialog) open = false
  }
</script>

<dialog bind:this={dialog} onclose={() => (open = false)} oncancel={handleCancel} onclick={handleClick}>
  <div class="dialog-head">{title}</div>
  <div class="dialog-body">
    {@render children?.()}
  </div>
  {#if footer}
    <div class="dialog-foot">
      {@render footer()}
    </div>
  {/if}
</dialog>
