// Modified 2026-11-04 by new-api-sub: APP=portal|admin dual entry, per-app routes/dist, /api proxy only, copy LICENSE/NOTICE
import path from 'node:path'
import { fileURLToPath } from 'node:url'

import { defineConfig } from '@rsbuild/core'
import { pluginReact } from '@rsbuild/plugin-react'
import { pluginTailwindcss } from '@rsbuild/plugin-tailwindcss'
import { tanstackRouter } from '@tanstack/router-plugin/rspack'

const __dirname = path.dirname(fileURLToPath(import.meta.url))

const APPS = {
  portal: { title: '小蓝号池', port: 5181, api: 'http://127.0.0.1:8081' },
  admin: {
    title: '小蓝号池管理后台',
    port: 5182,
    api: 'http://127.0.0.1:8082',
  },
} as const

export default defineConfig(({ envMode }) => {
  const app = process.env.APP
  if (app !== 'portal' && app !== 'admin') {
    throw new Error(
      `APP must be "portal" or "admin", got ${JSON.stringify(app)}`
    )
  }
  const cfg = APPS[app]
  const appDir = `./src/apps/${app}`

  const isProd = envMode === 'production'

  return {
    plugins: [pluginReact(), pluginTailwindcss({ optimize: false })],
    // Rsbuild 2: replaces deprecated `performance.chunkSplit` (RSPack 2 aligned)
    splitChunks: {
      preset: 'default',
      cacheGroups: {
        'vendor-react': {
          test: /node_modules[\\/](react|react-dom)[\\/]/,
          name: 'vendor-react',
          chunks: 'all',
          priority: 0,
          enforce: true,
        },
        'vendor-ui-primitives': {
          test: /node_modules[\\/](@base-ui|@radix-ui)[\\/]/,
          name: 'vendor-ui-primitives',
          chunks: 'all',
          priority: 0,
          enforce: true,
        },
        'vendor-tanstack': {
          test: /node_modules[\\/]@tanstack[\\/]/,
          name: 'vendor-tanstack',
          chunks: 'all',
          priority: 0,
          enforce: true,
        },
      },
    },
    source: {
      entry: {
        index: `${appDir}/main.tsx`,
      },
      // tsconfig.json references both apps; its paths would map @app to portal.
      tsconfigPath: `./tsconfig.${app}.json`,
    },
    resolve: {
      alias: {
        '@': path.resolve(__dirname, './src'),
        '@app': path.resolve(__dirname, appDir),
      },
    },
    html: {
      template: './index.html',
      title: cfg.title,
      favicon: './public/favicon.ico',
    },
    server: {
      host: '127.0.0.1',
      port: cfg.port,
      strictPort: true,
      // Go side enforces http.CrossOriginProtection, so keep the browser Origin/Host.
      // The key is a plain prefix: '/api' would also take the /api-tokens page.
      proxy: { '/api/': { target: cfg.api, changeOrigin: false } },
    },
    output: {
      // Production optimizations
      minify: isProd,
      target: 'web',
      distPath: {
        root: path.resolve(__dirname, `../${app}/dist`),
      },
      cleanDistPath: true,
      copy: [
        { from: './LICENSE' },
        { from: './NOTICE' },
        { from: './THIRD-PARTY-LICENSES.md' },
      ],
      // Rely on Rsbuild default legalComments ("linked" → per-chunk *.LICENSE.txt) in all modes.
      // Do not set "none" in production: that strips minifier-preserved third-party notices and
      // extracted license files, which some distributions require for open-source compliance.
    },
    performance: {
      // Remove console in production
      removeConsole: isProd ? ['log'] : false,
      buildCache: false,
    },
    tools: {
      rspack: {
        plugins: [
          tanstackRouter({
            target: 'react',
            routesDirectory: `${appDir}/routes`,
            generatedRouteTree: `${appDir}/routeTree.gen.ts`,
            // Dev: avoid per-route async chunks (reduces white flash on navigation + faster HMR feedback).
            // Prod: keep route-based code splitting.
            autoCodeSplitting: isProd,
          }),
        ],
      },
    },
  }
})
