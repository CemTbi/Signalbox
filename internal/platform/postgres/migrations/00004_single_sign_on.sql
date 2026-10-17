-- Single sign-on through OpenID Connect. Owned by internal/modules/identity.
-- See docs/adr/0013-single-sign-on-accounts.md.

-- +goose Up

-- An account at an identity provider, linked to a Signalbox account. The
-- provider's issuer and subject identify the person; the email address
-- they send is never used to find an account.
CREATE TABLE user_identities (
    issuer          text NOT NULL,
    subject         text NOT NULL,
    user_id         uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at      timestamptz NOT NULL DEFAULT now(),
    last_sign_in_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (issuer, subject),
    CONSTRAINT user_identities_subject_length CHECK (char_length(subject) BETWEEN 1 AND 255)
);

CREATE INDEX user_identities_user_id_idx ON user_identities (user_id);

-- A sign-in that went to the identity provider and hasn't come back yet.
-- Each one is used once and expires after a few minutes.
CREATE TABLE sso_attempts (
    -- SHA-256 of the OAuth state parameter.
    state_hash    bytea PRIMARY KEY,
    -- SHA-256 of the cookie that ties the attempt to the browser that
    -- started it, so a sign-in can't be finished in someone else's browser.
    browser_hash  bytea NOT NULL,
    nonce         text NOT NULL,
    -- The PKCE code verifier (RFC 7636).
    code_verifier text NOT NULL,
    return_path   text NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    expires_at    timestamptz NOT NULL,
    CONSTRAINT sso_attempts_hash_lengths CHECK (octet_length(state_hash) = 32 AND octet_length(browser_hash) = 32)
);

CREATE INDEX sso_attempts_expires_at_idx ON sso_attempts (expires_at);

-- +goose Down
DROP TABLE sso_attempts;
DROP TABLE user_identities;
