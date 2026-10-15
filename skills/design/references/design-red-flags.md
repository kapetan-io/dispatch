# Design red flags

Screen every candidate before synthesis. A red flag is a reason to revise or reject the shape.

## Ousterhout's four

- **Shallow module.** A large interface hiding little. Callers coordinate several methods for one operation, or options expose internal stages. Judge depth by the capability hidden behind the surface, and don't mistake a deep module for a deep call chain, which scatters understanding across layers.
- **Information leakage.** One internal decision appears in several modules, so changing it takes coordinated edits. Re-exporting transport or wire types is leakage. Parse external data into domain types behind the interface.
- **Temporal decomposition.** Modules split by execution order (load, validate, transform, save) repeat one representation and its invariants at every boundary. Group by the knowledge a module owns.
- **Pass-through method.** Forwards the same arguments to a method of the same shape. Remove it unless it adds policy or adaptation.

## Boundary below the domain

Testing the main behavior would mean scripting a dependency's output or asserting the exact request sent, so tests can only restate the implementation. Move the boundary up until tests read as situations: state set up in the domain's words, an action through the surface, a domain outcome asserted.

## Split ownership

More than one module writes the same state or keeps its own copy. An agent that edits one writer can't see the others, so their rules diverge. Give each piece of state one owner. Others read it or ask the owner to change it.

## Two ways to do one task

An agent copies whichever way it finds first, so every way keeps gaining callers. Keep one, move callers off the others and delete them in the same change.

## Importable internals

An agent takes the shortest path that compiles, so a caller that can import internals will, and they become interface. Make them unreachable so an outside import fails the build.

## Hand-synced list

Two places list the same items and adding one means editing both. An agent that sees one list updates only that one. Keep one list and derive the others, or fail the build when they disagree.
