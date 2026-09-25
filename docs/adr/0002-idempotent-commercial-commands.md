# Idempotent commercial commands

A backend response can be lost after a sale or return commits. Each submitted commercial intent therefore needs a stable request identity and must return the original outcome on retry. Sales already use `client_request_id`; returns need the same guarantee without creating a second return or stock change. The backend remains the authority for a confirmed outcome, while a client's unsynced attempt remains pending.
