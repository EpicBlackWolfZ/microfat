# Pinned SBOM schemas

These unmodified official schemas are embedded for offline validation. Generation
and verification never fetch schema references from the network.
Git preserves their original bytes, including the SPDX schema's upstream CRLF line endings.

- CycloneDX specification **1.7.2**, commit `349314a9d7671d7d2ca5b711a725f49a73979da6`,
  [schema directory](https://github.com/CycloneDX/specification/tree/349314a9d7671d7d2ca5b711a725f49a73979da6/schema).
  The document `specVersion` remains **1.7**.
- SPDX **3.0.1** [official JSON schema](https://spdx.org/schema/3.0.1/spdx-json-schema.json)
  and [JSON-LD context](https://spdx.org/rdf/3.0.1/spdx-context.jsonld), retrieved 2026-09-21.

SHA-256 pins:

```
73308edec3ab2d38bfffd993e96a042b594314143b6971a6e9ed98bbb6bd76ce  bom-1.7.schema.json
027b059a729a06d591bac79a584ef04f83fc32d91a826fdba6ad3c98a10e5b44  cryptography-defs.schema.json
8bae002c25e723db7ee1f26afde680ae1a2b1a8f6b4b4b0fd65dc3becb090aae  jsf-0.82.schema.json
4b345e2329f209f34e960ae2a8e7cb46a166907e6a45e94978565925dc47b359  spdx.schema.json
582c64e809d5b3ef9bd0c4de13a32391b47b0284a3e8d199569fb96f649234b1  spdx-3.0.1.schema.json
c72b0928f094c83e5c127784edb1ebca2af74a104fcacc007c332b23cbc788bd  spdx-3.0.1.context.jsonld
```

SPDX's schema uses ECMAScript negative lookahead. The validator uses the pinned
regexp2 engine with a one-second match bound, rather than rewriting the schema
for Go's narrower regexp syntax. Schema acceptance is followed by separate
reference, containment and independent archive/build-info inventory checks.
