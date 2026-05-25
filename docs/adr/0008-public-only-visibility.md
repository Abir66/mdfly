# All Documents are public; privacy is slug obscurity

mdfly does not support auth-gated, password-protected, or team-restricted Documents. Every published URL is fetchable by anyone who has it. "Private" sharing is achieved exclusively by the unguessability of the 8-character anonymous slug (62^8 ≈ 2 × 10^14 possible values, server-rate-limited against enumeration).

We chose this over building a viewer-side ACL because the entire view path is a static-CDN read — backend stays out of the hot path, viewers pay nothing per hit beyond R2/CDN egress, and link sharing matches the mental model of GitHub gists in "secret" mode. The cost is real: mdfly is unsuitable for sensitive content that must not be shared by accident (a forwarded URL = a permission grant), and we will not let this default drift back to "public" without a corresponding ADR.
