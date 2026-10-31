# provider

## What it answers

Which signature crate verifies which algorithm. These adapters are the only modules in `authn` that import `p256`, `p384`, `p521`, `ed25519-dalek` or `ring`; everything above speaks kit's `VerifyKey` and `Alg`. Wrong place for claims, key sets or refusals: that is `authn`.

## Use it when

- a signature crate is replaced or upgraded → change the one adapter (`ecdsa.rs`, `eddsa.rs`, `rsa.rs`)
- an algorithm is added → extend `Alg` and `VerifyKey` in `authn`, then the adapter that handles its key type

## Contract

- An adapter answers one question: does this signature verify over these bytes under this algorithm and key. Any malformed input is `false`; nothing panics.
- ECDSA signatures are the fixed-size `r || s` JWS form, never DER. Ed25519 uses `verify_strict` and refuses small-order keys at key construction.
- `rsa.rs` compiles its verifier only with `authn-rsa` (`ring`, 2048 to 8192 bit moduli); without it an RSA key is kept and verifies nothing.

## Neighbours

- `../`: the verifier, key sets and the public types
