# Changelog

## [1.0.0](https://github.com/jdziat/vastai-mcp/compare/v0.3.0...v1.0.0) (2026-09-05)


### ⚠ BREAKING CHANGES

* vast_create_ssh_key and vast_attach_ssh_key require confirmation; the -audit-log file is now raw JSONL without the AUDIT prefix; -max-dph now applies to vast_start_instance.

### Features

* 1.0 hardening from the final adversarial review ([3e56933](https://github.com/jdziat/vastai-mcp/commit/3e569330a69a99f62fd5eaa36650b41dd859bfb1))


### Bug Fixes

* **tools:** keep the storage-unknown guard on the bid path ([c038945](https://github.com/jdziat/vastai-mcp/commit/c038945879c9b832925db35801d893ad52770987))

## [0.3.0](https://github.com/jdziat/vastai-mcp/compare/v0.2.0...v0.3.0) (2026-09-05)


### ⚠ BREAKING CHANGES

* **tools:** vast_search_offers no longer accepts geolocation, min_inet_down_mbps, static_ip, min_direct_ports, or min_cuda; use raw_query.

### Features

* **tools:** trim vast_search_offers to core filters plus raw_query ([e805682](https://github.com/jdziat/vastai-mcp/commit/e805682c5846967a037dec6138dec4ac858f0a00))

## [0.2.0](https://github.com/jdziat/vastai-mcp/compare/v0.1.0...v0.2.0) (2026-09-05)


### Features

* **auth:** store the API key in the OS keyring ([e7473fc](https://github.com/jdziat/vastai-mcp/commit/e7473fcbaf07ec8ab07fd6661ac4df8987adcf35))


### Documentation

* **readme:** correct cosign identity (release job signs from refs/heads/main, not a tag ref) ([1702362](https://github.com/jdziat/vastai-mcp/commit/1702362b4ef58de87e0f1fe5106f1c7544dd9318))

## 0.1.0 (2026-09-05)


### Features

* **ci:** conventional commits and automated releases ([38f4d17](https://github.com/jdziat/vastai-mcp/commit/38f4d175c033060c91b5f683b8cd1cb665605c1e))
* **release:** sign checksums keylessly with cosign ([cffe7e2](https://github.com/jdziat/vastai-mcp/commit/cffe7e246bb7cb08f8ba8dbc88ce3a5d64acf8ed))


### Bug Fixes

* **release:** valid goreleaser config (template in flow sequence broke YAML parsing) ([36b0928](https://github.com/jdziat/vastai-mcp/commit/36b09282f49fe960904b1bbec6e78615d1bb245b))


### Documentation

* **remediation:** drop key-rotation follow-up (single-user workstation, not required) ([fe32efd](https://github.com/jdziat/vastai-mcp/commit/fe32efd5d6f0eaeb5b5fc6cf2456198a00a9c7d4))

## Changelog
