# Retired server bootstrap repository

Server bootstrap now lives beside Terraform at `git-repositories/infrastructure/iac/cloud-init/k3s.yaml.tftpl`. Keeping cloud-init in the IaC repository makes a standalone clone reproducible and prevents version skew between independently cloned repositories.

Do not add operational scripts or credentials here. Modify the IaC template and review the resulting Terraform replacement plan instead.