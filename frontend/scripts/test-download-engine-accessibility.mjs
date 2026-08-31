import { spawn } from 'node:child_process'
import { once } from 'node:events'
import { mkdtemp, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

const storybookUrl = process.env.STORYBOOK_URL ?? 'http://127.0.0.1:6006'
const storyUrl = `${storybookUrl}/iframe.html?id=settings-downloadenginepane--source-exceptions&viewMode=story`
const chromiumPath = process.env.CHROMIUM_PATH ?? '/usr/bin/chromium'
const profile = await mkdtemp(join(process.env.TMPDIR ?? tmpdir(), 'tsundoku-accessibility-'))

const formControlRoles = new Set([
  'checkbox',
  'combobox',
  'listbox',
  'radio',
  'searchbox',
  'slider',
  'spinbutton',
  'switch',
  'textbox',
])
const expectedDurationUnitNames = [
  'Refresh interval unit',
  'Download interval unit',
  'Chapter retry backoff unit',
  'Warm-up interval unit',
  'Source cooldown unit',
  'FlareSolverr request timeout unit',
  'FlareSolverr session TTL unit',
]
const expectedSpinbuttonNames = [
  'Refresh interval value',
  'Download interval value',
  'Chapter retry backoff value',
  'Chapter max retries',
  'Stale-grace days',
  'Refresh concurrency',
  'Download concurrency',
  'Max concurrent downloads',
  'Warm-up interval value',
  'Warm-up slow threshold',
  'Failure threshold',
  'Source cooldown value',
  'Politeness delay',
  'Image request delay',
  'FlareSolverr request timeout value',
  'FlareSolverr session TTL value',
  'CHAPTER CONCURRENCY OVERRIDE',
]

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
    if (result.result.value) return
    await new Promise(resolve => setTimeout(resolve, 50))
  }
  throw new Error(`Timed out waiting for browser expression: ${expression}`)
}

function readableNodes(nodes) {
  return nodes.map(node => ({
    role: node.role?.value ?? '',
    name: node.name?.value ?? '',
  }))
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
  await command('Accessibility.enable')

  const loaded = waitForEvent('Page.loadEventFired')
  await command('Page.navigate', { url: storyUrl })
  await loaded
  await poll(`Boolean(document.querySelector('[data-testid="download-engine-pane"]'))`)

  const tabContract = await command('Runtime.evaluate', {
    expression: `(async () => {
      const tablist = document.querySelector('[role="tablist"][aria-label="Download engine sections"]')
      const tabs = [...(tablist?.querySelectorAll('[role="tab"]') ?? [])]
      const panels = [...document.querySelectorAll('[role="tabpanel"]')]
      if (tabs.length !== 5 || panels.length !== 5) return { valid: false, reason: 'expected five linked tabs and panels' }
      const linked = tabs.every(tab => {
        const panel = document.getElementById(tab.getAttribute('aria-controls'))
        return panel?.getAttribute('aria-labelledby') === tab.id
      })
      tabs[0].focus()
      tabs[0].dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowLeft', bubbles: true }))
      const wrapped = document.activeElement === tabs[4] && tabs[4].getAttribute('aria-selected') === 'true'
      tabs[4].dispatchEvent(new KeyboardEvent('keydown', { key: 'Home', bubbles: true }))
      await new Promise(resolve => requestAnimationFrame(resolve))
      const homed = document.activeElement === tabs[0] && tabs[0].getAttribute('aria-selected') === 'true'
      return { valid: linked && wrapped && homed, linked, wrapped, homed }
    })()`,
    returnByValue: true,
    awaitPromise: true,
  })
  if (!tabContract.result.value.valid) {
    throw new Error(`Invalid tab contract: ${JSON.stringify(tabContract.result.value)}`)
  }

  const controls = []
  for (const panelId of [
    'download-engine-scheduling',
    'download-engine-protection',
    'download-engine-access',
    'download-engine-routing',
    'download-engine-source-exceptions',
  ]) {
    const activated = await command('Runtime.evaluate', {
      expression: `(() => {
        const tab = document.querySelector('[role="tab"][aria-controls="${panelId}"]')
        if (!tab) return false
        tab.click()
        if ('${panelId}' === 'download-engine-scheduling') document.querySelector('.advanced__toggle')?.click()
        return true
      })()`,
      returnByValue: true,
    })
    if (!activated.result.value) throw new Error(`Tab for ${panelId} is missing`)
    await poll(`!document.querySelector('#${panelId}').hidden`)
    if (panelId === 'download-engine-scheduling') {
      await poll(`document.querySelectorAll('#download-engine-scheduling input[type="number"]').length === 8`)
    }
    const { nodes } = await command('Accessibility.getFullAXTree')
    controls.push(...nodes.filter(node => !node.ignored && formControlRoles.has(node.role?.value)))
  }
  const unnamed = controls.filter(node => !(node.name?.value ?? '').trim())
  const durationUnitNames = controls
    .filter(node => node.role?.value === 'combobox' && (node.name?.value ?? '').endsWith(' unit'))
    .map(node => node.name.value)
  const spinbuttonNames = controls
    .filter(node => node.role?.value === 'spinbutton')
    .map(node => node.name.value)

  const failures = []
  if (unnamed.length) {
    failures.push(`visible unnamed form controls: ${JSON.stringify(readableNodes(unnamed))}`)
  }
  if (JSON.stringify(durationUnitNames) !== JSON.stringify(expectedDurationUnitNames)) {
    failures.push(`duration unit names: ${JSON.stringify(durationUnitNames)}`)
  }
  if (new Set(durationUnitNames).size !== durationUnitNames.length) {
    failures.push(`duration unit names are not unique: ${JSON.stringify(durationUnitNames)}`)
  }
  if (JSON.stringify([...spinbuttonNames].sort()) !== JSON.stringify([...expectedSpinbuttonNames].sort())) {
    failures.push(`spinbutton names: ${JSON.stringify(spinbuttonNames)}`)
  }
  if (new Set(spinbuttonNames).size !== spinbuttonNames.length) {
    failures.push(`spinbutton names are not unique: ${JSON.stringify(spinbuttonNames)}`)
  }
  if (failures.length) throw new Error(failures.join('\n'))

  console.log(JSON.stringify({
    chromium: await command('Browser.getVersion'),
    visibleFormControls: controls.length,
    visibleUnnamedFormControls: unnamed.length,
    spinbuttonNames,
    durationUnitNames,
  }, null, 2))
}
finally {
  socket?.close()
  if (chromium && chromium.exitCode == null) {
    chromium.kill('SIGTERM')
    await once(chromium, 'exit')
  }
  await rm(profile, { recursive: true, force: true, maxRetries: 4, retryDelay: 50 })
}
