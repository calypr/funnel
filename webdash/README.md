# Funnel web dashboard

The dashboard uses React, Vite, and Vitest. It requires Node.js 22.13+, 24+, or 26+; see the `engines` field in `package.json` for the supported ranges.

Run `npm ci` to install the locked dependencies. Run `npm start` to start the development server at [http://localhost:3000](http://localhost:3000). Run `npm test` to run the Vitest suite.

Run `npm run build` to write production files to `build/`, or run `make webdash` from the repository root. Commit the updated `webdash/build/` assets. The Go server embeds those files with `go:embed` when the binary is compiled. Application assets go in `build/static/`, matching the server's `/static/` handler.

The dashboard frontend GitHub Actions workflow runs the tests and production build on pull requests and pushes to `develop`. It also fails if the fresh build differs from the committed assets, including added or removed files. After building locally, run `bash .github/scripts/check-webdash-bundle.sh` from the repository root to perform the same check.
