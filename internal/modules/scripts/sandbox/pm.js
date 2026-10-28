// SPDX-License-Identifier: Apache-2.0

// The Postman script sandbox, built on the libraries Postman itself uses:
// pm.request and pm.response are postman-collection objects, and pm.expect
// is chai with Postman's chai-postman assertions. Go calls __execute once per
// script with a plain context and reads plain results back; scripts.go
// describes both.
//
// Asynchronous work (pm.sendRequest, setTimeout, tests with a done
// callback) is queued in `pending`. Go drains the queue after the script's
// body returns, which is the event loop.

const libs = globalThis.__libs;

const modules = {
  ajv: () => libs.ajv,
  buffer: () => libs.buffer,
  chai: () => libs.chai,
  "crypto-js": () => libs["crypto-js"],
  lodash: () => libs.lodash,
  moment: () => libs.moment,
  "postman-collection": () => libs["postman-collection"],
  tv4: () => libs.tv4,
  uuid: () => libs.uuid,
  url: () => libs.url,
  xml2js: () => libs.xml2js,
  atob: () => atob,
  btoa: () => btoa,
};

function atob(s) {
  return libs.buffer.Buffer.from(String(s), "base64").toString("binary");
}

function btoa(s) {
  return libs.buffer.Buffer.from(String(s), "binary").toString("base64");
}

// Postman's xml2Json: the object xml2js makes, with attributes under $.
function xml2Json(xml) {
  let result;
  libs.xml2js.parseString(xml, { async: false, explicitArray: false }, (err, r) => {
    if (err) throw err;
    result = r;
  });
  return result;
}

// Postman's older sandbox let scripts write responseBody.has("text").
if (!String.prototype.has) {
  Object.defineProperty(String.prototype, "has", {
    configurable: true,
    writable: true,
    value: function (s) {
      return this.indexOf(s) !== -1;
    },
  });
}

function requireModule(name) {
  const load = modules[name];
  if (!load) {
    throw new Error(`the module "${name}" isn't available in Signalbox scripts`);
  }
  return load();
}

let chaiReady = false;
function chai() {
  const c = libs.chai;
  if (!chaiReady) {
    const sdk = libs["postman-collection"];
    c.use(libs["chai-postman"](sdk, libs.lodash, libs.ajv));
    // As in Postman, pm.response.to.have... and pm.request.to... start an
    // assertion about the object.
    for (const type of [sdk.Response, sdk.Request]) {
      Object.defineProperty(type.prototype, "to", {
        configurable: true,
        get() {
          return c.expect(this).to;
        },
      });
    }
    chaiReady = true;
  }
  return c;
}

// A variable scope with Postman's methods over a plain object.
function scope(name, values, changes) {
  const has = (key) => Object.prototype.hasOwnProperty.call(values, key);
  return {
    has: (key) => has(String(key)),
    get: (key) => (has(String(key)) ? values[String(key)] : undefined),
    set(key, value) {
      key = String(key);
      values[key] = value === undefined || value === null ? value : typeof value === "object" ? value : value;
      changes.push({ scope: name, op: "set", key });
    },
    unset(key) {
      key = String(key);
      delete values[key];
      changes.push({ scope: name, op: "unset", key });
    },
    clear() {
      for (const key of Object.keys(values)) delete values[key];
      changes.push({ scope: name, op: "clear" });
    },
    toObject: () => Object.assign({}, values),
    replaceIn: (template) => replaceIn(template, [values]),
  };
}

function replaceIn(template, layers) {
  if (typeof template !== "string") return template;
  return template.replace(/\{\{([^{}]+)\}\}/g, (match, key) => {
    for (const layer of layers) {
      if (Object.prototype.hasOwnProperty.call(layer, key)) {
        const v = layer[key];
        return typeof v === "object" ? JSON.stringify(v) : String(v);
      }
    }
    return match;
  });
}

function format(args) {
  return args
    .map((a) => {
      if (typeof a === "string") return a;
      if (a instanceof Error) return String(a);
      try {
        return JSON.stringify(a);
      } catch (_) {
        return String(a);
      }
    })
    .join(" ");
}

function errorInfo(e) {
  if (e && typeof e === "object") {
    return { name: e.name || "Error", message: e.message !== undefined ? String(e.message) : String(e) };
  }
  return { name: "Error", message: String(e) };
}

globalThis.__execute = function (ctx, compiled) {
  const sdk = libs["postman-collection"];
  const out = { tests: [], logs: [], changes: [], nextRequest: undefined, skipRequest: false, error: undefined };
  const pending = [];
  const vars = ctx.variables;
  // pm.variables reads the narrowest scope first.
  const layers = [vars.local, vars.data, vars.environment, vars.collection, vars.globals];

  const test = (name, fn) => {
    const result = { name: String(name), passed: true, skipped: false, error: undefined };
    out.tests.push(result);
    const fail = (e) => {
      result.passed = false;
      result.error = errorInfo(e);
    };
    if (typeof fn !== "function") {
      result.skipped = true;
      return;
    }
    try {
      if (fn.length > 0) {
        // The test finishes when it calls done, maybe later.
        result.pending = true;
        fn((e) => {
          result.pending = false;
          if (e) fail(e);
        });
      } else {
        const r = fn();
        if (r && typeof r.then === "function") {
          result.pending = true;
          r.then(
            () => (result.pending = false),
            (e) => {
              result.pending = false;
              fail(e);
            },
          );
        }
      }
    } catch (e) {
      fail(e);
    }
  };
  test.skip = (name) => out.tests.push({ name: String(name), passed: true, skipped: true });

  let request;
  let response;
  const pm = {
    info: ctx.info,
    test,
    expect: (...a) => chai().expect(...a),
    environment: scope("environment", vars.environment, out.changes),
    collectionVariables: scope("collection", vars.collection, out.changes),
    globals: scope("globals", vars.globals, out.changes),
    iterationData: scope("data", vars.data, out.changes),
    variables: Object.assign(scope("local", vars.local, out.changes), {
      has: (key) => layers.some((l) => Object.prototype.hasOwnProperty.call(l, key)),
      get(key) {
        for (const l of layers) if (Object.prototype.hasOwnProperty.call(l, key)) return l[key];
        return undefined;
      },
      toObject: () => Object.assign({}, ...layers.slice().reverse()),
      replaceIn: (template) => replaceIn(template, layers),
    }),
    get request() {
      if (!request) {
        chai();
        request = new sdk.Request(ctx.request);
      }
      return request;
    },
    get response() {
      if (!response && ctx.response) {
        chai(); // installs pm.response.to
        response = new sdk.Response(ctx.response);
      }
      return response;
    },
    sendRequest(req, callback) {
      const op = { kind: "send", request: typeof req === "string" ? { url: req, method: "GET" } : toPlainRequest(sdk, req) };
      return new Promise((resolve, reject) => {
        op.done = (err, res) => {
          const r = res ? new sdk.Response(res) : undefined;
          if (typeof callback === "function") {
            try {
              callback(err ? new Error(err) : null, r);
            } catch (e) {
              out.error = out.error || errorInfo(e);
            }
          }
          if (err) reject(new Error(err));
          else resolve(r);
        };
        pending.push(op);
      });
    },
    execution: {
      // null stops the run; it travels as "", which no request is named.
      setNextRequest: (name) => (out.nextRequest = name === null ? "" : String(name)),
      skipRequest: () => (out.skipRequest = true),
    },
    require: requireModule,
  };
  pm.expect.fail = (msg) => chai().expect.fail(msg);

  // The globals of Postman's older sandbox, still common in collections.
  const res = ctx.response;
  const headerObject = {};
  for (const h of (res && res.header) || []) headerObject[h.key] = h.value;
  const legacy = {
    tests: {},
    responseBody: res ? res.body : undefined,
    responseCode: res ? { code: res.code, name: res.status, detail: res.status } : undefined,
    responseTime: res ? res.responseTime : undefined,
    responseHeaders: headerObject,
    globals: vars.globals,
    environment: vars.environment,
    data: vars.data,
    iteration: ctx.info.iteration,
    request: {
      url: ctx.request.url,
      method: ctx.request.method,
      headers: Object.fromEntries((ctx.request.header || []).map((h) => [h.key, h.value])),
      data: ctx.request.body && ctx.request.body.raw,
    },
  };
  const postman = {
    setEnvironmentVariable: (k, v) => pm.environment.set(k, v),
    getEnvironmentVariable: (k) => pm.environment.get(k),
    clearEnvironmentVariable: (k) => pm.environment.unset(k),
    clearEnvironmentVariables: () => pm.environment.clear(),
    setGlobalVariable: (k, v) => pm.globals.set(k, v),
    getGlobalVariable: (k) => pm.globals.get(k),
    clearGlobalVariable: (k) => pm.globals.unset(k),
    clearGlobalVariables: () => pm.globals.clear(),
    setNextRequest: (name) => pm.execution.setNextRequest(name),
    getResponseHeader(name) {
      const want = String(name).toLowerCase();
      for (const h of (res && res.header) || []) if (h.key.toLowerCase() === want) return h.value;
      return undefined;
    },
    getResponseCookie: () => undefined,
  };

  const log = (level) => (...a) => out.logs.push({ level, message: format(a) });
  const console = { log: log("log"), info: log("info"), warn: log("warn"), error: log("error"), debug: log("debug") };
  const setTimeout = (fn, ms, ...args) => {
    pending.push({ kind: "timer", delay: Number(ms) || 0, done: () => fn(...args) });
    return pending.length;
  };

  const globals = {
    pm,
    postman,
    console,
    setTimeout,
    require: requireModule,
    xml2Json,
    get _() {
      return libs.lodash;
    },
    get CryptoJS() {
      return libs["crypto-js"];
    },
    get tv4() {
      return libs.tv4;
    },
    get moment() {
      return libs.moment;
    },
    atob,
    btoa,
    ...legacy,
  };

  try {
    compiled(globals);
  } catch (e) {
    out.error = errorInfo(e);
  }

  // The test results of the old sandbox: every key of `tests` is a test.
  for (const name of Object.keys(legacy.tests)) {
    out.tests.push({ name, passed: Boolean(legacy.tests[name]), skipped: false });
  }

  return {
    out,
    pending,
    finish() {
      for (const t of out.tests) {
        if (t.pending) {
          t.passed = false;
          t.error = { name: "Error", message: "the test didn't finish before the script ended" };
        }
        delete t.pending;
      }
      if (request) out.request = toPlainRequest(sdk, request);
      out.variables = vars;
      return out;
    },
  };
};

function toPlainRequest(sdk, req) {
  const r = sdk.Request.isRequest(req) ? req : new sdk.Request(req);
  const json = r.toJSON();
  return {
    url: r.url.toString(),
    method: r.method,
    header: (json.header || []).filter((h) => !h.disabled).map((h) => ({ key: h.key, value: h.value })),
    body: plainBody(json.body),
  };
}

// A body as Go reads it: raw is always text, as Postman sends it.
function plainBody(body) {
  if (!body) return undefined;
  const out = Object.assign({}, body);
  if (out.raw !== undefined && typeof out.raw !== "string") out.raw = JSON.stringify(out.raw);
  for (const mode of ["urlencoded", "formdata"]) {
    if (Array.isArray(out[mode])) {
      out[mode] = out[mode].map((p) => ({ key: String(p.key), value: p.value === undefined ? "" : String(p.value), disabled: Boolean(p.disabled) }));
    }
  }
  return out;
}
