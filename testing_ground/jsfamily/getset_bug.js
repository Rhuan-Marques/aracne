// EDGE / KNOWN-BUG PROBE: a getter and setter with the SAME property name give
// the accessor pair one method ID, and populateClassMethods appends it twice
// without de-duping — a duplicate has_method connection that violates
// UNIQUE(source_id, conn_type, target_id) and aborts THIS file's entire topology
// write (the file still lands on disk, so disk and topology diverge). Kept
// ISOLATED so the abort cannot taint other fixtures.
// SHOULD: Thermostat + a single `temp` accessor resource exist with no abort.
// ACTUAL (observe live): write may abort; class/methods missing from topology.
export class Thermostat {
  #temp = 20;

  get temp() {
    return this.#temp;
  }

  set temp(v) {
    this.#temp = v;
  }
}
