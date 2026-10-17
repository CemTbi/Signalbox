// Node globals some bundled libraries expect.
export { Buffer } from "buffer";
export const process = { env: {}, nextTick: (fn, ...args) => Promise.resolve().then(() => fn(...args)), version: "", versions: {}, browser: true };
export const global = globalThis;
