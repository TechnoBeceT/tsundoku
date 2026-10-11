const realFunctionPrototype = Function.prototype;
class URL {
    constructor(value, base) {
        this.href = value.startsWith('http') ? value : base.slice(0, base.lastIndexOf('/') + 1) + value.replace(/^\.\//, '');
        const parts = this.href.match(/^(https?):\/\/([^/]+)/);
        this.protocol = parts[1] + ':';
        this.origin = parts[1] + '://' + parts[2];
    }
}
var discoveryResult = 'PENDING';
const check = (condition, label) => { if (!condition) throw new Error(label); };
async function execute(kind) {
    let finish;
    const done = new Promise(resolve => { finish = resolve; });
    const calls = [];
    const raw = { id: 7200400, pages: { alt: ['images.example'], node: 'node', items: [{ url: 'https://images.example/1.webp', s: 1 }, { url: 'https://images.example/2.webp', s: 0 }] } };
    const client = { get: async(path, options) => { calls.push({ path, options }); return { data: raw }; } };
    const getter = async(a,b)=>(await client.get(a,b)).data;
    const route = a=>`/chapters/${a}`;
    let environment = { mt: { Vk: getter }, pt: { mL: { qR: route, like:a=>`/chapters/${a}/like` }, user: { state:a=>`/user/chapters/${a}/state` } },
        query: { qR: () => { throw new Error('Arbitrary function invoked'); } } };
    if (kind === 'missing') environment = {};
    if (kind === 'duplicate-get') environment.extra = { get: getter };
    if (kind === 'duplicate-route') environment.extra = { chapter: { detail: route } };
    if (kind === 'wrong-route') environment.pt = { mL: { qR:a=>`/chapters/${a}/like` } };
    const mockFunction = function () { return async () => environment; };
    mockFunction.prototype = realFunctionPrototype;
    Object.assign(globalThis, { Function: mockFunction,
        window: { pass: value => finish({ value: JSON.parse(value) }), reject: error => finish({ error: String(error.message || error) }) },
        location: { href: 'https://comix.to/title/title/7200400-chapter-77', pathname: kind === 'bad-location' ? '/title/title/not-a-chapter' : '/title/title/7200400-chapter-77', origin: 'https://comix.to' },
        document: { querySelector: selector => selector === 'script#initial-data' ? null : { src: kind === 'foreign-main' ? 'https://foreign.example/main.js' : 'https://comix.to/dist/main.js' } },
        fetch: async () => ({ ok: true, text: async () => kind === 'unsafe-import' ? 'import{x}from"https://foreign.example/api.js";' : 'import{x}from"./api.js";' }) });
    if (kind === 'early') {
        const readyDocument = document;
        const pass = window.pass, reject = window.reject;
        globalThis.document = { querySelector: () => null };
        (0, eval)(callbackScript);
        check(!window.__comixPagePayload && calls.length === 0, 'Early DOM must remain unarmed');
        globalThis.document = readyDocument;
        window.pass = undefined;
        (0, eval)(callbackScript);
        check(!window.__comixPagePayload && calls.length === 0, 'Missing pass bridge must remain unarmed');
        window.pass = pass;
        window.reject = undefined;
        (0, eval)(callbackScript);
        check(!window.__comixPagePayload && calls.length === 0, 'Missing reject bridge must remain unarmed');
        window.reject = reject;
    }
    (0, eval)(callbackScript);
    (0, eval)(callbackScript);
    return { ...await done, calls, raw };
}
(async () => {
    const early = await execute('early');
    check(!early.error && early.calls.length === 1, 'Ready same-window retry must succeed exactly once');
    const result = await execute('current');
    check(!result.error, result.error);
    check(JSON.stringify(result.value) === JSON.stringify({ result: result.raw }), 'Raw page DTO changed');
    check(result.calls.length === 1 && result.calls[0].path === '/chapters/7200400', 'Wrong chapter request');
    check(result.value.result.pages.baseUrl === undefined, 'Invented base URL');
    for (const kind of ['missing', 'duplicate-get', 'duplicate-route', 'wrong-route', 'bad-location', 'foreign-main', 'unsafe-import']) {
        const failure = await execute(kind);
        check(failure.error && failure.calls.length === 0, `${kind} must refuse before transport`);
    }
    discoveryResult = 'PASS';
})().catch(error => { discoveryResult = String(error.stack || error); });
