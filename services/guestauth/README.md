# Guestauth

The `guestauth` service is responsible for creating and validating guest login tokens and exchanging them for a session cookie.

It is part of the default service set. It does not need to be explicitly enabled with `OC_ADD_RUN_SERVICES`.

## Token lifecycle

- When a guest share is created (`ShareCreated` event), the service generates a secure random token, persists it and emits a `GuestTokenCreated` event.
- When a guest share is removed or expires (`ShareRemoved` / `ShareExpired` events),the service cleans up the stored token.
- The token can be redeemed via `POST /graph/v1beta1/guestInvitations/redeem` to exchange it for a session cookie.

## Configuration

The service can be configured via environment variables (prefix `GUESTAUTH_*`)or a `guestauth.yaml` configuration file.

To run the service without consuming events, set `GUESTAUTH_EVENTS_DISABLED=true`. To run it without the HTTP service, set `GUESTAUTH_HTTP_DISABLED=true`.