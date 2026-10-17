// The libraries a Postman script can use, then the sandbox itself. Each
// library is loaded the first time a script asks for it, so a script pays
// only for what it uses. build.mjs bundles this file into sandbox.js.
const libs = {};
const lazy = (name, load) => Object.defineProperty(libs, name, { enumerable: true, get: load });
lazy("chai", () => require("chai"));
lazy("chai-postman", () => require("chai-postman"));
lazy("postman-collection", () => require("postman-collection"));
lazy("lodash", () => require("lodash"));
lazy("crypto-js", () => require("crypto-js"));
lazy("moment", () => require("moment"));
lazy("ajv", () => require("ajv"));
lazy("tv4", () => require("tv4"));
lazy("uuid", () => require("uuid"));
lazy("buffer", () => require("buffer"));
lazy("xml2js", () => require("xml2js"));
lazy("url", () => require("url"));
globalThis.__libs = libs;
require("./pm.js");
