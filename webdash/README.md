# Funnel web dashboard

The dashboard uses React, Vite, and Vitest. It requires Node.js 22.13+, 24+, or 26+; see the `engines` field in `package.json` for the supported ranges.

Run `npm ci` to install the locked dependencies. Run `npm start` to start the development server at [http://localhost:3000](http://localhost:3000). Run `npm test` to run the Vitest suite.

Run `npm run build` to write production files to `build/`. From the repository root, run `make webdash` to embed the files in the Go server. The build places application assets in `build/static/`, which matches the server's `/static/` handler.
