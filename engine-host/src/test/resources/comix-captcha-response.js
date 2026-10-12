// Runs the transformed released callback, with the current site's pending CAPTCHA interceptor.
const realFunctionPrototype = Function.prototype;
class URL {
    constructor(value, base = 'https://comix.to/title/xkry7') {
        value = String(value);
        const origin = String(base).match(/^https?:\/\/[^/]+/)[0];
        this.href = value.startsWith('http') ? value : value.startsWith('/') ? origin + value :
            String(base).slice(0, String(base).lastIndexOf('/') + 1) + value.replace(/^\.\//, '');
        const parts = this.href.match(/^(https?):\/\/([^/]+)([^?#]*)/);
        this.protocol = parts[1] + ':';
        this.origin = parts[1] + '://' + parts[2];
        this.pathname = parts[3] || '/';
    }
}
const check = (condition, label) => { if (!condition) throw new Error(label); };
const flush = async () => { for (let i = 0; i < 32; i++) await Promise.resolve(); };
var discoveryResult = 'PENDING';

async function execute(kind) {
    const calls = [], requests = [], deliveries = [];
    let opened;
    const transportStarted = new Promise(resolve => { opened = resolve; });
    const path = pageCallback ? '/chapters/7200400' : '/manga/xkry7/chapters';
    const expectedUrl = 'https://comix.to/api/v1' + path;
    const raw = pageCallback ? { id: 7200400, pages: { items: [{ url: 'https://images.example/1.webp', s: 1 }] } } :
        { items: [{ id: 1, number: 73, name: 'Original title', group: { id: 12, name: 'Original group' } }], meta: { lastPage: 1 } };
    class XMLHttpRequest {
        constructor() { this.listeners = new Map(); requests.push(this); }
        open(method, url, ...args) { this.method = method; this.url = url; this.openArgs = args; }
        addEventListener(type, listener) {
            if (!this.listeners.has(type)) this.listeners.set(type, new Set());
            this.listeners.get(type).add(listener);
        }
        removeEventListener(type, listener) { this.listeners.get(type)?.delete(listener); }
        getResponseHeader(name) { return name.toLowerCase() === 'content-type' ? this.contentType : null; }
        send(body) { this.sentBody = body; }
        complete({ status = 403, contentType = 'application/json', data = { error: 'captcha_required', challenge: 'private-challenge' }, responseType = 'text' } = {}) {
            this.status = status;
            this.contentType = contentType;
            this.responseType = responseType;
            this.responseURL = this.url;
            this.responseText = typeof data === 'string' ? data : JSON.stringify(data);
            this.response = responseType === 'json' ? data : this.responseText;
            this.readyState = 4;
            for (const type of ['readystatechange', 'load', 'loadend']) {
                for (const listener of [...(this.listeners.get(type) || [])]) listener.call(this, { target: this });
            }
        }
    }
    const originalOpen = XMLHttpRequest.prototype.open;
    const originalSend = XMLHttpRequest.prototype.send;
    let resolveResponse, actual;
    const client = { get: async (route, options) => {
        calls.push({ route, options });
        // Unrelated physical requests must neither trigger a bridge nor be rewritten.
        if (kind.startsWith('unrelated-')) {
            const decoy = new XMLHttpRequest();
            const url = kind === 'unrelated-origin' ? 'https://foreign.example/api/v1' + path :
                kind === 'unrelated-id' ? expectedUrl.replace(pageCallback ? '7200400' : 'xkry7', 'other') :
                kind === 'unrelated-path' ? expectedUrl + '/like' : expectedUrl;
            decoy.open(kind === 'unrelated-method' ? 'POST' : 'GET', url, true, 'unchanged');
            decoy.send('original-body');
            decoy.complete(kind === 'unrelated-error' ? { data: { error: 'other_error' } } :
                kind === 'unrelated-status' ? { status: 401 } :
                kind === 'unrelated-nonjson' ? { contentType: 'text/html', data: '<html>captcha_required</html>' } : {});
            check(decoy.url === url && decoy.sentBody === 'original-body' && decoy.openArgs[1] === 'unchanged', 'Unrelated request changed');
        }
        actual = new XMLHttpRequest();
        actual.open('GET', 'https://comix.to/api/v1' + route + '?page=1&limit=100', true);
        actual.send(null);
        opened();
        if (kind === 'captcha-text' || kind === 'captcha-json') {
            // Mirrors the captured client: the network finishes, but CAPTCHA UI owns its promise.
            return await new Promise(() => {});
        }
        return await new Promise(resolve => { resolveResponse = resolve; });
    } };
    const getter = async(a,b)=>(await client.get(a,b)).data;
    const route = pageCallback ? a=>`/chapters/${a}` : a=>`/manga/${a}/chapters`;
    const environment = { api: { get: getter }, routes: { chapter: { list: route } } };
    const mockFunction = function () { return async () => environment; };
    mockFunction.prototype = realFunctionPrototype;
    Object.assign(globalThis, { Function: mockFunction, XMLHttpRequest,
        window: { XMLHttpRequest, pass: value => deliveries.push({ value: JSON.parse(value) }),
            reject: error => deliveries.push({ error: String(error.message || error) }) },
        location: { href: 'https://comix.to/title/xkry7/7200400-chapter-77', origin: 'https://comix.to', pathname: '/title/xkry7/7200400-chapter-77' },
        document: { querySelector: () => ({ src: 'https://comix.to/dist/main.js' }) },
        fetch: async () => ({ ok: true, text: async () => 'import{x}from"./api.js";' }) });
    (0, eval)(callbackScript);
    await transportStarted;
    await flush();
    check(deliveries.length === 0, 'Bridge completed before physical transport');
    let replacement;
    if (kind === 'newer-wrapper') {
        const prior = XMLHttpRequest.prototype.open;
        replacement = function (...args) { return prior.apply(this, args); };
        XMLHttpRequest.prototype.open = replacement;
    }
    actual.complete(kind === 'captcha-json' ? { responseType: 'json' } :
        kind === 'captcha-text' ? {} : { status: 200, data: raw });
    resolveResponse?.({ data: raw });
    await flush();
    check(deliveries.length === 1, `${kind}: expected exactly one bridge result, received ${deliveries.length}`);
    const result = deliveries[0];
    if (kind.startsWith('captcha-')) {
        check(result.error === 'Comix requires a human CAPTCHA verification', `${kind}: missing sanitized CAPTCHA rejection`);
        check(!result.error.includes('private-challenge'), 'Challenge payload leaked');
    } else {
        check(!result.error, `${kind}: ordinary success rejected`);
        check(JSON.stringify(result.value) === JSON.stringify(pageCallback ? { result: raw } : raw.items), 'Raw response changed');
    }
    check(calls.length === 1 && calls[0].route === path, 'Signed getter request changed');
    check(XMLHttpRequest.prototype.open === (replacement || originalOpen), 'Open hook not restored or newer wrapper clobbered');
    check(XMLHttpRequest.prototype.send === originalSend, 'Send method changed');
    check(requests.every(xhr => [...xhr.listeners.values()].every(listeners => listeners.size === 0)), 'Temporary listeners retained');
}

(async () => {
    for (const kind of ['captcha-text', 'captcha-json', 'success', 'unrelated-origin', 'unrelated-id', 'unrelated-path',
        'unrelated-method', 'unrelated-error', 'unrelated-status', 'unrelated-nonjson', 'newer-wrapper']) await execute(kind);
    discoveryResult = 'PASS';
})().catch(error => { discoveryResult = String(error.stack || error); });
