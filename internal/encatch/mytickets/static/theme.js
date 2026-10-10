// encatch: My Tickets light/dark switch without a page reload. The server renders the
// theme from the same cookie on every page, so there is no flash; without this script
// the switch is a plain form that sets the cookie on the server.
(function () {
  var COOKIE = 'libredesk_my_tickets_theme'

  document.addEventListener('submit', function (e) {
    var form = e.target
    var button = e.submitter
    if (!form.classList.contains('theme-switch') || !button || button.name !== 'theme') return
    e.preventDefault()

    var theme = button.value === 'light' || button.value === 'dark' ? button.value : ''
    var secure = location.protocol === 'https:' ? '; Secure' : ''
    document.cookie = COOKIE + '=' + theme + '; Path=/my-tickets; SameSite=Lax' + secure +
      (theme ? '; Max-Age=31536000' : '; Max-Age=0')

    if (theme) document.documentElement.setAttribute('data-theme', theme)
    else document.documentElement.removeAttribute('data-theme')

    // The switch appears twice (sidebar and mobile menu): keep both in step.
    var buttons = document.querySelectorAll('form.theme-switch button[name="theme"]')
    for (var i = 0; i < buttons.length; i++) {
      buttons[i].setAttribute('aria-pressed', String(buttons[i].value === (theme || 'system')))
    }
  })
})()
