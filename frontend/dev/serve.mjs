import { parseArgs } from 'node:util'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'

const { values } = parseArgs({ options: {
  host: { type: 'string', default: '127.0.0.1' },
  port: { type: 'string', default: '4317' },
  strictPort: { type: 'boolean', default: true },
} })
if (values.host !== '127.0.0.1') throw new Error('Console preview only binds to 127.0.0.1')
const port = Number(values.port)
if (!Number.isInteger(port) || port < 1 || port > 65535) throw new Error('Invalid preview port')
const server = await createServer({
  root: fileURLToPath(new URL('../', import.meta.url)),
  configFile: fileURLToPath(new URL('../vite.config.ts', import.meta.url)),
  mode: 'console-preview', envFile: false,
  server: { host: values.host, port, strictPort: true },
})
await server.listen()
server.printUrls()
for (const signal of ['SIGINT', 'SIGTERM']) {
  process.once(signal, async () => { await server.close(); process.exit(0) })
}
