# Funnel web dashboard

The dashboard uses React, Vite, and Vitest. Use a Node.js version supported by the `engines` field in [package.json](package.json).

This directory contains the frontend source, development tools, and tests. The private Go package in [`internal/webdash`](../internal/webdash/) embeds and serves the compiled assets. Its `build/` directory contains generated files.

Run `npm ci` to install the locked dependencies. Run `npm start` to start the development server at [http://localhost:3000](http://localhost:3000). Run `npm test` to run the Vitest suite.

Run `npm run build` from this directory, or run `make webdash` from the repository root. Both commands write production files to `internal/webdash/build/`, relative to the repository root. Commit the updated files in that directory. The Go server embeds those files with `go:embed` when the binary is compiled. JavaScript and CSS files go in `internal/webdash/build/static/`, matching the server's `/static/` handler.

The dashboard frontend GitHub Actions workflow runs the tests and production build on pull requests and pushes to `develop`. It also fails if the fresh build differs from the committed assets, including added or removed files. After building locally, run `bash .github/scripts/check-webdash-bundle.sh` from the repository root to perform the same check.
