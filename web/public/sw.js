// Minimal offline support: cache the app shell, network-first for the API.
const SHELL = 'leafwise-shell-v1'
self.addEventListener('install', (e) => {
  e.waitUntil(caches.open(SHELL).then((c) => c.addAll(['/', '/index.html', '/manifest.webmanifest', '/icon.svg'])))
  self.skipWaiting()
})
self.addEventListener('activate', (e) => e.waitUntil(self.clients.claim()))
self.addEventListener('fetch', (e) => {
  const url = new URL(e.request.url)
  if (e.request.method !== 'GET') return
  if (url.pathname.startsWith('/api/')) {
    e.respondWith(
      fetch(e.request)
        .then((res) => {
          const copy = res.clone()
          caches.open('leafwise-api').then((c) => c.put(e.request, copy))
          return res
        })
        .catch(() => caches.match(e.request)),
    )
    return
  }
  e.respondWith(caches.match(e.request).then((hit) => hit || fetch(e.request)))
})
