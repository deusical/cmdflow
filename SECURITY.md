# Security policy

## Supported versions

Only the latest release receives fixes while cmdflow is pre-1.0.

## Reporting a vulnerability

Please don't open a public issue. Report it privately through GitHub's
[security advisory form](https://github.com/deusical/cmdflow/security/advisories/new).
Include what you found, how to reproduce it and the impact you expect. You'll
get a reply within a week.

Of particular interest: any way configuration can cause code to run that
isn't a registered module, an effect to happen without passing through
`Host.Effect` (for modules the policy doesn't allow to act directly), or a
running command to change when its configuration is edited.
