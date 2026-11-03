`run.sh` copies the CA named by `HOOPDEV_CA_CERT` here as `extra-ca.crt`, and
the dev image trusts it. A cloud sandbox needs it: it re-signs TLS. Empty on a
developer machine, where the image build skips it.
