import { spawn } from 'node:child_process'
import { once } from 'node:events'
import { mkdtemp, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

const storybookUrl = process.env.STORYBOOK_URL ?? 'http://127.0.0.1:6006'
const storyUrl = `${storybookUrl}/iframe.html?id=settings-downloadenginepane--narrow&viewMode=story`
const chromiumPath = process.env.CHROMIUM_PATH ?? '/usr/bin/chromium'
const profile = await mkdtemp(join(process.env.TMPDIR ?? tmpdir(), 'tsundoku-responsive-'))

let chromium
let socket
let nextMessageId = 0
const pending = new Map()
const eventWaiters = new Map()

function waitForEvent(method) {
  return new Promise((resolve) => {
    const waiters = eventWaiters.get(method) ?? []
    waiters.push(resolve)
    eventWaiters.set(method, waiters)
  })
}

function connect(endpoint) {
  return new Promise((resolve, reject) => {
    socket = new WebSocket(endpoint)
    socket.addEventListener('open', resolve, { once: true })
    socket.addEventListener('error', reject, { once: true })
    socket.addEventListener('message', ({ data }) => {
      const message = JSON.parse(data)
      if (message.id) {
        const waiter = pending.get(message.id)
        pending.delete(message.id)
        if (message.error) waiter?.reject(new Error(message.error.message))
        else waiter?.resolve(message.result)
        return
      }
      const waiters = eventWaiters.get(message.method) ?? []
      eventWaiters.delete(message.method)
      for (const waiter of waiters) waiter(message.params)
    })
  })
}

function command(method, params = {}) {
  const id = ++nextMessageId
  return new Promise((resolve, reject) => {
    pending.set(id, { resolve, reject })
    socket.send(JSON.stringify({ id, method, params }))
  })
}

async function poll(expression, timeoutMs = 15_000) {
  const deadline = Date.now() + timeoutMs
  while (Date.now() < deadline) {
    const result = await command('Runtime.evaluate', {
      expression,
      returnByValue: true,
      awaitPromise: true,
    })
    if (result.result.value) return result.result.value
    await new Promise(resolve => setTimeout(resolve, 50))
  }
  throw new Error(`Timed out waiting for browser expression: ${expression}`)
}

async function measure(width) {
  await command('Emulation.setDeviceMetricsOverride', {
    width,
    height: 1_000,
    deviceScaleFactor: 1,
    mobile: false,
  })
  const loaded = waitForEvent('Page.loadEventFired')
  await command('Page.navigate', { url: `${storyUrl}&viewport=${width}` })
  await loaded
  await poll(`Boolean(document.querySelector('[data-testid="download-engine-pane"]'))`)

  const evaluation = await command('Runtime.evaluate', {
    expression: `(() => {
      const size = (element) => ({
        clientWidth: element.clientWidth,
        scrollWidth: element.scrollWidth,
      })
      const pane = document.querySelector('[data-testid="download-engine-pane"]')
      const routing = document.querySelector('#download-engine-routing')
      const card = routing.querySelector('.surface-card')
      const endpointRows = [...routing.querySelectorAll('.ep-row')]
      const endpointTexts = [...routing.querySelectorAll('.ep-row__name, .ep-row__summary')]
        .map((element) => {
          const style = getComputedStyle(element)
          return {
            ...size(element),
            whiteSpace: style.whiteSpace,
            textOverflow: style.textOverflow,
          }
        })
      const paneRight = pane.getBoundingClientRect().right
      const overflows = [...routing.querySelectorAll('*')]
        .filter(element => element.getBoundingClientRect().right > paneRight + 0.5)
        .slice(0, 12)
        .map(element => ({
          element: element.tagName.toLowerCase() + (element.className ? '.' + String(element.className).trim().replaceAll(' ', '.') : ''),
          clientWidth: element.clientWidth,
          scrollWidth: element.scrollWidth,
          renderedWidth: Math.round(element.getBoundingClientRect().width),
        }))
      const visibleCrudButtons = [...routing.querySelectorAll('button')]
        .filter(button => button.getBoundingClientRect().width > 0).length
      return {
        viewport: window.innerWidth,
        document: size(document.documentElement),
        body: size(document.body),
        pane: size(pane),
        routing: size(routing),
        card: size(card),
        endpointRows: endpointRows.map(size),
        endpointTexts,
        overflows,
        visibleCrudButtons,
      }
    })()`,
    returnByValue: true,
  })
  return evaluation.result.value
}

function assertFits(dimensions) {
  const failures = []
  for (const key of ['document', 'body', 'pane', 'routing', 'card']) {
    const box = dimensions[key]
    if (box.scrollWidth > box.clientWidth) {
      failures.push(`${key} ${box.clientWidth}/${box.scrollWidth}`)
    }
  }
  dimensions.endpointRows.forEach((box, index) => {
    if (box.scrollWidth > box.clientWidth) {
      failures.push(`endpointRows[${index}] ${box.clientWidth}/${box.scrollWidth}`)
    }
  })
  if (dimensions.viewport === 375) {
    dimensions.endpointTexts.forEach((box, index) => {
      if (box.scrollWidth > box.clientWidth || box.whiteSpace === 'nowrap' || box.textOverflow === 'ellipsis') {
        failures.push(`endpointTexts[${index}] truncates essential text`)
      }
    })
  }
  if (dimensions.visibleCrudButtons < 5) failures.push('endpoint CRUD controls are not all visible')
  if (failures.length) {
    throw new Error(`${dimensions.viewport}px overflow: ${failures.join(', ')}\n${JSON.stringify(dimensions)}`)
  }
}

try {
  await fetch(storybookUrl, { signal: AbortSignal.timeout(2_000) })

  const debuggerEndpoint = await new Promise((resolve, reject) => {
    chromium = spawn(chromiumPath, [
      '--headless=new',
      '--no-sandbox',
      '--disable-gpu',
      '--remote-debugging-address=127.0.0.1',
      '--remote-debugging-port=0',
      `--user-data-dir=${profile}`,
      'about:blank',
    ], { stdio: ['ignore', 'ignore', 'pipe'] })

    const timeout = setTimeout(() => reject(new Error('Chromium did not expose its debugger endpoint')), 10_000)
    chromium.stderr.setEncoding('utf8')
    chromium.stderr.on('data', (chunk) => {
      const match = chunk.match(/DevTools listening on (ws:\/\/[^\s]+)/)
      if (!match) return
      clearTimeout(timeout)
      resolve(match[1])
    })
    chromium.once('error', reject)
    chromium.once('exit', code => reject(new Error(`Chromium exited before testing (code ${code})`)))
  })

  const debuggerPort = new URL(debuggerEndpoint).port
  const targets = await fetch(`http://127.0.0.1:${debuggerPort}/json/list`).then(response => response.json())
  const pageTarget = targets.find(target => target.type === 'page')
  if (!pageTarget) throw new Error('Chromium did not create a page target')

  await connect(pageTarget.webSocketDebuggerUrl)
  await command('Page.enable')
  await command('Runtime.enable')

  const results = []
  for (const width of [375, 768]) {
    const dimensions = await measure(width)
    results.push(dimensions)
    assertFits(dimensions)
  }
  console.log(JSON.stringify(results, null, 2))
}
finally {
  socket?.close()
  if (chromium?.exitCode == null) {
    chromium?.kill('SIGTERM')
    await once(chromium, 'exit')
  }
  await rm(profile, { recursive: true, force: true, maxRetries: 4, retryDelay: 50 })
}
