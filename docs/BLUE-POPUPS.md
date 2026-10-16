# Blue trophy popups

The Windows Vigil-style trophy notification now uses a navy background (`#0B1522`), a quiet pale-blue
identity stripe (`#8FC7E8`), clear white text (`#E9EFF4`) and muted blue secondary text (`#A8BFD3`).
Platinum has a slightly brighter navy surface (`#14243A`) and keeps its silver outline. The gold,
silver, bronze and platinum trophy images and grade labels remain distinct; they were not recoloured.

This updates the real GDI+ renderer, not a mockup. The existing placement, timing, sound, queue,
long-name wrapping and Windows notification style are preserved. No gameplay folders, user
configuration or running agent executable were touched.

The existing English and Portuguese screenshots in `docs/screenshots/agent/` and the four images
used on the Download page were rendered from the updated code at 2× resolution. Tests render every
grade and summary at 100%, 125%, 150% and 200% scaling, check broken-icon fallback, long names,
rounded transparency, blue accents and WCAG AA text contrast on both background colours.

Claude's completed website redesign was integrated without conflicts. The new renderer is distributed
through the official Actions build of v0.1.0-beta.2, copied unchanged to the public distribution
repository with four matching checksums and anonymous download verification. Beta.1 remains immutable.

The web download targets beta.2; the footer links to the public distribution repository. The privacy
introduction now describes an independent project, without changing its pending controller/contact
decisions. Installation illustrations and trophy medals retain their existing metallic artwork;
native popup samples use the actual blue renderer. A release-binary render matches the committed sample.

Production verification caught restrictive permissions on copied public files: Caddy could not read
some images/favicon. The Dockerfile normalizes only /srv directories to 755 and public files to 644.
CI builds the real web image with a deliberately restrictive public folder and checks readability as
its non-root user. Application secrets and persistent volumes are outside this normalization.
