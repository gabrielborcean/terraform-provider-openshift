# ACME dev — minimal, no auto-install (validate config before touching hardware)
openshift_version = "4.14"
api_vip           = "10.1.0.10"
ingress_vip       = "10.1.0.11"
auto_install      = false   # generate ISO, review hosts before triggering install
