# Rendered by `make test-conformance` into api-key.yaml (git-ignored, removed
# after the run). Only $FREENS_API_KEY is substituted.
apiVersion: v1
kind: Secret
metadata:
  name: freens-api-key
type: Opaque
stringData:
  api-key: "${FREENS_API_KEY}"
