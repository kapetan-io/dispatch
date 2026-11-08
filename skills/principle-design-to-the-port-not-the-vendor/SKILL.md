---
name: principle-design-to-the-port-not-the-vendor
description: "Apply when writing or changing a port's contract, adding a product or a delivery mechanism behind one, naming a config table or glossary term after a product, or writing code that branches on which product or source is in use."
---

# Design to the Port, Not the Vendor

Hexagonal architecture, applied in both directions. Nothing in the core can tell which product sits behind an outbound port, and nothing in the core can tell which adapter (timer, event stream, command line, test) delivered a fact or command through an inbound one.

**Why:** A second vendor should cost one adapter. If the first vendor's shape has leaked into names, config and rules, it costs a rewrite instead. The inbound side is the one that gets forgotten.

- **Name the port for the role it fills in the domain,** not for the first product that filled it or for a term that product uses.
- **Configuration follows the same boundary.** An adapter's settings live with that adapter, never under another product's key, and selecting the active adapter is one mechanism shared by every port. Two adapters claiming one port is a configuration error, not a default.
- **A fact is one fact once the core holds it,** however it arrived. A consumer that asks whether it came by poll or by event is reading the adapter, not the port.
- **A branch on the vendor outside the port is a leak.** Code above the port that needs to know which product or delivery path is in use means the port is missing something or the adapter is doing too little. Move the decision inside the adapter that owns it. "Socket missing, so type" is the adapter's call, if it is anyone's.
- **A new adapter inherits** the existing adapters and the shared contract tests, and differs only where the new product forces it.
- **A contract the new product cannot meet is a finding against the port, so stop building.** Bring the designer the failing contract test and why the product cannot pass it. The port, every adapter and the shared test change together, never a carve-out for one vendor or a weakened test for one adapter.
