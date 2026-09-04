# Brand assets

| File | What it is |
|---|---|
| `logo.png` | 329×360 — the README header. Use this one. |
| `aracne.png` | 1254×1254 master, with border |
| `aracne_borderless.png` | 898×981 master, transparent background |
| `icon.png` | 1254×1254 icon master, with border |
| `icon_borderless.png` | 1184×1125 icon master, transparent background |

The visualizer's own copies live in `internal/viz/static/` (`favicon.png` 64px,
`logo.png` 128px) because they are `go:embed`ed into the binary and must stay small.

These were previously also checked in as `.svg`, but each of those was a
`<svg><image href="data:image/png;base64,...">` wrapper around the PNG beside it — a
raster ~33% larger than the file it hid, not a vector. They carried no artwork the PNGs
do not, so they were removed. If a true vector is ever drawn, it belongs here.
