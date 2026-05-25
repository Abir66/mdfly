# Object storage: Cloudflare R2

Bundle files (rendered markdown, Assets, OG-tagged HTML shell) are stored in Cloudflare R2 and served to viewers through the Cloudflare CDN.

We chose R2 over S3+CloudFront and Backblaze B2 primarily because viewer traffic is read-heavy across the world and R2 charges zero egress, while S3 charges roughly $0.09/GB out. R2's S3-compatible API keeps the door open to migrating if Cloudflare's reliability or pricing changes. The cost is mild Cloudflare lock-in (Workers/CDN tightly coupled if we adopt edge logic later) and a slightly less mature ops surface than S3.
