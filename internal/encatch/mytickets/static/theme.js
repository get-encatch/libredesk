// encatch: My Tickets light/dark toggle without a page reload. The server renders the
// theme from the same cookie on every page, so there is no flash; without this script
// the toggle is a plain form that sets the cookie on the server.
(function () {
  var COOKIE = 'libredesk_my_tickets_theme'
  var device = window.matchMedia('(prefers-color-scheme: dark)')

  document.addEventListener('submit', function (e) {
    var form = e.target
    if (!form.classList.contains('theme-switch')) return
    e.preventDefault()

    // Flip what is shown now. Matching the device again means following it (no cookie).
    var root = document.documentElement
    var saved = root.getAttribute('data-theme')
    var showingDark = saved ? saved === 'dark' : device.matches
    var wantDark = !showingDark
    var theme = wantDark === device.matches ? '' : (wantDark ? 'dark' : 'light')

    var secure = location.protocol === 'https:' ? '; Secure' : ''
    document.cookie = COOKIE + '=' + theme + '; Path=/my-tickets; SameSite=Lax' + secure +
      (theme ? '; Max-Age=31536000' : '; Max-Age=0')
    if (theme) root.setAttribute('data-theme', theme)
    else root.removeAttribute('data-theme')
  })
})()
