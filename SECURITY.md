# Reporting a vulnerability

Report privately through [GitHub security advisories](https://github.com/noetive/riffle/security/advisories/new) or by email to **security@noetive.eu**. Please do not open a public issue, and do not include a working credential in the report.

Useful to include: what an attacker gains, the smallest reproduction you have, and the version from `riffle version`.

We aim to acknowledge within two working days and to ship a fix or a mitigation within thirty days for anything in scope below. We will credit you in the release notes unless you prefer otherwise.

## Scope

In scope:

- **Secret handling.** Any way a password, token, cookie or other secret given to Riffle reaches the agent, a log, a view, an error message or disk in readable form.
- **Kept sign-ins.** A session started with `-keep-state` keeps its cookies and site storage on disk on purpose, in a file only its user can read. In scope: any way another user, a page or a session that did not ask for that state can read or use it, any way it is written where it is not readable by its user only, and any way a kept sign-in reaches a session whose limits refuse its site.
- **Navigation and upload policy.** Any way a page, or a program, gets Riffle to visit an address or read or send a local file that the configured policy forbids.
- **Page text as instructions.** Any way text from a web page is presented to the agent as something other than page content, so that the page can steer the agent.
- **Release integrity.** Published binaries and packages that do not verify against their signature and build provenance.

Out of scope: vulnerabilities in the browser Riffle drives (report those to its vendor), and behaviour of a site that Riffle displays accurately.

## Supported versions

Only the latest published release. There is no back-porting.
