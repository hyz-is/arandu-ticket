# Upgrade Guide

## Unreleased

## v0.1.0

First release: nothing to upgrade from. A new installation reads the README, in
this order: `go get`, the wiring (`TeamTenant` is the one required setting), the
actions in the arandu-permission catalogue, `aru migrate`, and
`aru vendor:publish` with `aru view:build` for the screens. The package requires
Framework v0.66.0, Hesape v0.85.0, arandu-activitylog v0.7.0 and
arandu-markdown v0.4.0, and Go 1.26.10.

