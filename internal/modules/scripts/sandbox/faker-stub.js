// postman-collection uses faker only for {{$random...}} dynamic variables,
// which Signalbox resolves itself, so faker isn't bundled. Reading any
// property works, as postman-collection does while it loads; calling a
// faker function fails.
const unavailable = new Proxy(function () {}, {
  get: () => unavailable,
  apply() {
    throw new Error("dynamic variables such as {{$randomName}} are resolved before the script runs");
  },
});
module.exports = unavailable;
