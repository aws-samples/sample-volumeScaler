#!/bin/bash

# Copyright 2024 The Kubernetes Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# This script deletes a cluster that was created by `create-cluster.sh`
# CLUSTER_NAME and CLUSTER_TYPE are expected to be specified by the caller
# All other environment variables have default values (see config.sh) but
# many can be overridden on demand if needed

set -euo pipefail

BASE_DIR="$(dirname "$(realpath "${BASH_SOURCE[0]}")")"
BIN="${BASE_DIR}/../../bin"

source "${BASE_DIR}/config.sh"
source "${BASE_DIR}/util.sh"

if [ -z "${HELM_VALUES_FILE:-}" ]; then
  if [[ "${CLUSTER_TYPE}" == "kops" ]]; then
    HELM_VALUES_FILE="${BASE_DIR}/kops/values.yaml"
  elif [[ "${CLUSTER_TYPE}" == "eksctl" ]]; then
    HELM_VALUES_FILE="${BASE_DIR}/eksctl/values.yaml"
  else
    echo "Cluster type ${CLUSTER_TYPE} is invalid, must be kops or eksctl" >&2
    exit 1
  fi
fi

loudecho "Installing driver via ${DEPLOY_METHOD}"
install_driver
loudecho "Sucessfully installed driver via ${DEPLOY_METHOD}"
