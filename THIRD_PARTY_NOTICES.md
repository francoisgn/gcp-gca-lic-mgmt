# Third-party notices

| Component | Usage | License |
|---|---|---|
| [Chart.js](https://www.chartjs.org) 4.5.0 | Vendored in `internal/web/static/vendor/` (charts of the observability page) | MIT — see `internal/web/static/vendor/chart.js-LICENSE.md` |
| [Catppuccin](https://catppuccin.com) Mocha palette | Colour values of the design system (`app.css`, `DESIGN.md`), style of the illustration | MIT |
| Roboto, Open Sans, Lato, Roboto Mono | Loaded at runtime from Google Fonts (not redistributed) | Apache-2.0 / SIL OFL 1.1 |
| Go modules listed in `go.mod` (Google Cloud client libraries, pgx, yaml.v3, golang.org/x/…) | Compiled into the binary | Apache-2.0, BSD-3-Clause or MIT — see each module |

`go-licenses report ./...` lists the exact license of every Go dependency.
