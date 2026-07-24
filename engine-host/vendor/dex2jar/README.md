# Vendored dex2jar fix — `NewTransformer` (GAP-100 (d))

`NewTransformer.java` is a single vendored class from the dex2jar fork
`de.femtopedia.dex2jar:dex-ir:2.4.37` (originally `com.googlecode.dex2jar.ir.ts.NewTransformer`),
carrying a one-method fix that upstream does not yet have. The build recompiles just this class and
overlays it into the shipped jar — **no binary jar is checked into git.**

## The defect

dex2jar translates Android DEX bytecode to JVM bytecode when the engine-host loads a Mihon
extension APK. `NewTransformer` merges the two-step allocation

```
a = NEW Abc;
a.<init>();
```

into a single `a = new Abc()`. To do so it needs the instantiated type. The original code took it
from `InvokeExpr.getOwner()` — the class that declares the `<init>` being called.

That is wrong for R8/keiyoushi-optimised extensions. When R8 collapses an allocation, the
constructor resolves to `java/lang/Object.<init>`, so `getOwner()` returns `java/lang/Object` and the
concrete type is **erased**. The result is a `new java/lang/Object` that later fails a `checkcast` —
a `ClassCastException` at runtime (e.g. casting to `okhttp3.Interceptor`). Real symptoms: Toonily and
Vortex Scans fail to load or throw on use.

The `NewExpr` created by the `NEW` instruction always carries the real instance type in `NewExpr.type`
(correct in both the normal and the collapsed cases), so the fix reads that instead of `getOwner()`.

## The fix (two lines, one per merge path)

1. **`replaceAST(...)`** — the `if (newExpr != null)` block:

   ```java
   -   InvokeExpr invokeNew = Exprs.nInvokeNew(nOps, ie.getArgs(), ie.getOwner());
   +   InvokeExpr invokeNew = Exprs.nInvokeNew(nOps, ie.getArgs(), newExpr.type);
   ```

2. **`replace0(...)`** — the loop over `init.values()`. Here the invoke's receiver is still the
   `Local`, not the `NewExpr`, so the type must come from the object's own allocation assignment
   (`a = NEW Abc`), which is `obj.init.getOp2()`:

   ```java
   +   NewExpr newExpr = (NewExpr) obj.init.getOp2();
   -   InvokeExpr invokeNew = Exprs.nInvokeNew(nOps, ie.getArgs(), ie.getOwner());
   +   InvokeExpr invokeNew = Exprs.nInvokeNew(nOps, ie.getArgs(), newExpr.type);
   ```

The rest of the file is the fork's own source, with the fork's field names (`Local.lsIndex`,
`Stmt.cfgFroms`) so it compiles against the resolved fork jar.

## Why vendored (not forked / not a binary)

The fix is a single method in a single class. Forking the whole dex2jar tree, or checking a patched
binary jar into git, would be far heavier than the change warrants. Instead the build recompiles only
this class against the already-resolved fork jar and overlays the result — so the tree stays clean and
the fix is reproducible from source. Remove this vendor directory (and the Gradle wiring) once upstream
carries the equivalent change.

## How the build applies it

`engine-host/build.gradle.kts` (search `GAP-100 (d)`):

1. **`compilePatchedDex2jar`** (a `JavaCompile` task) resolves `dex-ir-2.4.37.jar` from the runtime
   classpath and compiles `NewTransformer.java` against it with `--release 21` (via the pinned
   Java-21 toolchain).
2. **`patchInstalledDex2jar`** overlays the recompiled
   `com/googlecode/dex2jar/ir/ts/NewTransformer*.class` (the outer class plus its nested `$1`, `$1$1`,
   `$TObject`, `$Vx`) into the copy `installDist` places in `build/install/tsundoku-engine-host/lib/`,
   using `jar uf` (an unconditional entry replace).
3. `installDist` is `finalizedBy` the overlay, and the overlay is `upToDateWhen { false }` so it
   re-applies on every install (installDist always lays down a pristine copy of the fork jar).

## Verifying the shipped jar carries the fix

```
jar=$(find engine-host/build/install/tsundoku-engine-host/lib -name 'dex-ir-2.4.37.jar')
unzip -p "$jar" com/googlecode/dex2jar/ir/ts/NewTransformer.class > /tmp/NewTransformer.class
javap -p -c /tmp/NewTransformer.class | grep -B1 nInvokeNew
```

Both `Exprs.nInvokeNew` callsites must be fed by `getfield … NewExpr.type` — **zero** `getOwner`
calls preceding an `nInvokeNew`.
