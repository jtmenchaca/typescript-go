# Pointing Cursor / VS Code at the forked `tsgo --lsp`

The forked language server serves refinement judgments
(`source: "refinedts"`, codes 7001–7005), hover spellings, and
quickfixes on live buffers — no classic tsserver plugin involved
(the classic `typescriptServerPlugins` contribution is not loaded
under TS7/tsgo).

1. Build the binary:

   ```bash
   pnpm tsgo:build
   ```

2. Native Preview's tsdk resolver wants a binary named **`tsgo`**
   (not `tsgo-bin`); the symlink is already in this directory and
   gitignored:

   ```bash
   ln -sf tsgo-bin packages/refinedts/refined-ts-go/tsgo
   ```

3. Install the TypeScript Native Preview extension, then set (User
   settings — workspace tsdk needs trust + Allow):

   ```jsonc
   {
     "js/ts.experimental.useTsgo": true,
     "js/ts.tsdk.path": "/abs/path/to/packages/refinedts/refined-ts-go"
   }
   ```

   Legacy spelling: `typescript.native-preview.tsdk` (same resolver).

The kernel dylib resolves from the binary's own location
(`cmd/tsgo/refinedts_kernel.go`), so `pnpm kernel:native` must have built
`refined-ts-lean/native/build/librefinedts_kernel.dylib`; without it
the server still runs, and every kernel-gated judgment declines.
(`pnpm kernel` only builds the static `kernel:static` intermediate.)
Surface recognition is discovery: any program file ending in
`/surface/z.ts`.

`pnpm refinedts:install` (the classic VSIX) is NOT part of this path.
