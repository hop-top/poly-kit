// Package integrations is the parent of cross-cutting adapters
// surfaced by adopter reviews. It holds no code of its own.
//
// Sub-packages:
//
//   - [hop.top/kit/go/integrations/repohost] — unified repository-host
//     SPI with drivers for github, gitlab, gitea, gitee and bitbucket,
//     each with a sibling mock/ sub-package.
package integrations
