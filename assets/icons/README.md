# grimoire — brand icons

App/site icons generated from the project artwork (`source-grimoire.jpeg`).
The master is `icon-master-1024.png` (1024×1024, opaque); every asset here is
derived from it, so regenerate from the master to keep sizes consistent.

## Files

| File | Purpose |
|------|---------|
| `favicon.ico` | Multi-resolution favicon (16/32/48/64) |
| `favicon-16x16.png`, `favicon-32x32.png` | PNG favicons |
| `apple-touch-icon.png` (180×180) | iOS/iPadOS home-screen icon |
| `icon-152.png`, `icon-167.png`, `icon-180.png` | iOS touch-icon size variants |
| `android-chrome-192x192.png`, `android-chrome-512x512.png` | PWA / Android |
| `icon-{16,32,48,64,128,256,512,1024}.png` | General-purpose PNG sizes |
| `grimoire.icns` | macOS app-bundle icon (`AppIcon.icns`) |
| `site.webmanifest` | PWA manifest referencing the icons |
| `icon-master-1024.png` | 1024² master used to regenerate all sizes |
| `source-grimoire.jpeg` | Original source artwork (provenance) |

## Wiring (public site `<head>`)

When the web static handler is implemented (M1 Phase: web/routing), serve this
directory at `/assets/icons/` and add:

```html
<link rel="icon" href="/assets/icons/favicon.ico" sizes="any">
<link rel="icon" type="image/png" sizes="32x32" href="/assets/icons/favicon-32x32.png">
<link rel="icon" type="image/png" sizes="16x16" href="/assets/icons/favicon-16x16.png">
<link rel="apple-touch-icon" sizes="180x180" href="/assets/icons/apple-touch-icon.png">
<link rel="manifest" href="/assets/icons/site.webmanifest">
```

## Regenerating

All assets derive from `icon-master-1024.png` via Pillow + `iconutil`
(`favicon.ico` multi-size, `grimoire.icns` from an `.iconset`).
