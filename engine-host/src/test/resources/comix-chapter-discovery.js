// Exercised by ComixCompatibilityTest against the transformed released callback.
const realFunctionPrototype = Function.prototype;
// GraalJS has no browser URL global; the transport fixture uses these fixed HTTP URLs.
class URL {
    constructor(value, base) {
        this.href = value.startsWith('http') ? value : base.slice(0, base.lastIndexOf('/') + 1) + value.replace(/^\.\//, '');
        const parts = this.href.match(/^(https?):\/\/([^/]+)/);
        this.protocol = parts[1] + ':';
        this.origin = parts[1] + '://' + parts[2];
    }
}
const assert = {
    equal: (actual, expected, label = '') => { if (actual !== expected) throw new Error(`${label}: ${actual} != ${expected}`); },
    deepEqual: (actual, expected) => { if (JSON.stringify(actual) !== JSON.stringify(expected)) throw new Error(`Unexpected value: ${JSON.stringify(actual)}`); },
    ok: (value, label = '') => { if (!value) throw new Error(label || 'Expected truthy value'); }
};
var discoveryResult = 'PENDING';
async function execute(kind, { duplicate = '', malformed = '', latest = null, pages = 3, withDecoys = false } = {}) {
    const calls = [];
    const client = { get: async (path, options) => {
        calls.push({ path, ...options.params });
        return { data: { items: [{ id: options.params.page, number: 73, name: 'Chapter title', votes: 4, createdAtFormatted: '2d', isOfficial: true, group: { id: 12, name: 'Example' }, url: '/chapter' }], meta: { lastPage: pages } } };
    } };
    // These function bodies are the prior/current site's raw wrappers and route builders.
    const prior = { T: { get: async(a,b)=>(await client.get(a,b)).data }, w: { manga: { chapters:a=>`/manga/${a}/chapters` } } };
    const current = { mt: { Vk: async(a,b)=>(await client.get(a,b)).data }, pt: { Eg: { mL:a=>`/manga/${a}/chapters` } },
        c: { mL: async () => ({ LG: [], aZ: {} }) } };
    const renamed = { differentClient: { renamed: async(title,options)=>(await client.get(title,options)).data },
        differentRoutes: { differentGroup: { renamed:title=>`/manga/${title}/chapters` } } };
    // Query-key methods and transformed DTO APIs must never be invoked during discovery.
    const decoys = { query: { chapters: () => { throw new Error('arbitrary function invoked'); } },
        key: { manga: { chapters:a=>['manga', 'chapters', a] } }, otherRoute: { manga: { chapters:a=>`/groups/${a}/chapters` } } };
    let environment = { ...({ prior, current, renamed }[kind] || {}), ...(withDecoys ? decoys : {}) };
    if (duplicate === 'get') environment.extra = { x: async(a,b)=>(await client.get(a,b)).data };
    if (duplicate === 'route') environment.extra = { x: { y:a=>`/manga/${a}/chapters` } };
    if (malformed === 'get') environment = { ...current, mt: { Vk: async(a,b)=>(await client.get(a,a)).data } };
    if (malformed === 'route-space') environment = { ...current, pt: { Eg: { mL:a=>`/manga/ ${a}/chapters` } } };
    if (malformed === 'route') environment = { ...current, pt: { Eg: { mL:a=>`/manga/${a}/chapters?unsafe=1` } } };
    if (kind === 'split') environment = { ...decoys, ...{ T: prior.T } };
    let finish;
    const done = new Promise(resolve => { finish = resolve; });
    const importMock = function () { return async url => url.endsWith('route-only.js') ? { w: prior.w } : environment; };
    importMock.prototype = realFunctionPrototype;
    Object.assign(globalThis, { location: { href: 'https://comix.to/title/xkry7', origin: 'https://comix.to' },
        window: { pass: value => finish({ value: JSON.parse(value) }), reject: error => finish({ error: error.message }) },
        Function: importMock, fetch: async () => ({ ok: true, text: async () => 'import{x}from"./api.js";' + (kind === 'split' ? 'import{x}from"./route-only.js";' : '') }) });
    (0, eval)(callbackScript.replace('const latestChapterId = null;', `const latestChapterId = ${latest};`));
    return { ...await done, calls };
}

(async () => {
    for (const kind of ['current', 'prior', 'renamed']) {
        const result = await execute(kind);
        assert.equal(result.error, undefined, kind);
        assert.equal(result.value.length, 3, kind);
        assert.deepEqual(result.value[0], { id: 1, number: 73, name: 'Chapter title', votes: 4, createdAtFormatted: '2d', isOfficial: true, group: { id: 12, name: 'Example' }, url: '/chapter' });
        for (const call of result.calls) assert.deepEqual(call, { path: '/manga/xkry7/chapters', page: call.page, limit: 100, order: { number: 'desc' } });
        assert.equal((await execute(kind, { withDecoys: true })).calls.length, 3);
        assert.equal((await execute(kind, { latest: 1 })).calls.length, 1);
        assert.equal((await execute(kind, { pages: 500 })).calls.length, 200);
    }
    for (const [kind, options] of [['missing', {}], ['split', {}], ['current', { duplicate: 'get' }], ['current', { duplicate: 'route' }],
        ['current', { malformed: 'get' }], ['current', { malformed: 'route' }], ['current', { malformed: 'route-space' }]]) {
        const result = await execute(kind, options);
        assert.ok(result.error, `${kind} ${JSON.stringify(options)}`);
        assert.equal(result.calls.length, 0);
    }
    discoveryResult = 'PASS';
})().catch(error => { discoveryResult = String(error.stack || error); });
