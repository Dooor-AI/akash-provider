ifeq (, $(AP_DEVCACHE))
$(error AP_DEVCACHE is not set)
endif

ifeq (, $(AKASH))
$(error "AKASH variable is not set")
endif

$(AP_DEVCACHE):
	@echo "creating .cache dir structure..."
	mkdir -p $@
	mkdir -p $(AP_DEVCACHE_BIN)
	mkdir -p $(AP_DEVCACHE_INCLUDE)
	mkdir -p $(AP_DEVCACHE_VERSIONS)
	mkdir -p $(AP_DEVCACHE_NODE_MODULES)
	mkdir -p $(AP_DEVCACHE_TESTS)
	mkdir -p $(AP_DEVCACHE)/run

.INTERMEDIATE: cache
cache: $(AP_DEVCACHE)

AKASH_INSTALL_ARCH := $(UNAME_ARCH)
# darwin has option to install multi-arch binary
ifeq ($(UNAME_OS_LOWER), darwin)
	AKASH_INSTALL_ARCH := all
endif

.PHONY: akash-rm
akash-rm:
	rm -f $(AKASHD_VERSION_FILE)

.PHONY: akash
ifeq ($(AKASHD_BUILD_FROM_SRC), true)
akash:
	@echo "compiling and installing Akash from local sources"
	$(AP_ROOT)/script/tools.sh build-akash $(AKASHD_LOCAL_PATH)
#make -C $(AKASHD_LOCAL_PATH) akash AKASH=$(AP_DEVCACHE_BIN)/akash
else
$(AKASHD_VERSION_FILE): $(AP_DEVCACHE)
	@echo "Installing akash $(AKASHD_VERSION) ..."
	rm -f $(AKASH)
	wget -q https://github.com/akash-network/node/releases/download/v$(AKASHD_VERSION)/akash_$(UNAME_OS_LOWER)_$(AKASH_INSTALL_ARCH).zip -O $(AP_DEVCACHE)/akash.zip
	unzip -p $(AP_DEVCACHE)/akash.zip akash > $(AKASH)
	chmod +x $(AKASH)
	rm $(AP_DEVCACHE)/akash.zip
	rm -rf "$(dir $@)"
	mkdir -p "$(dir $@)"
	touch $@
akash: akash-rm $(AKASHD_VERSION_FILE)
endif

$(STATIK_VERSION_FILE): $(AP_DEVCACHE)
	@echo "Installing statik $(STATIK_VERSION) ..."
	rm -f $(STATIK)
	GOBIN=$(AP_DEVCACHE_BIN) $(GO) install github.com/rakyll/statik@$(STATIK_VERSION)
	rm -rf "$(dir $@)"
	mkdir -p "$(dir $@)"
	touch $@
$(STATIK): $(STATIK_VERSION_FILE)

$(GIT_CHGLOG_VERSION_FILE): $(AP_DEVCACHE)
	@echo "installing git-chglog $(GIT_CHGLOG_VERSION) ..."
	rm -f $(GIT_CHGLOG)
	GOBIN=$(AP_DEVCACHE_BIN) go install github.com/git-chglog/git-chglog/cmd/git-chglog@$(GIT_CHGLOG_VERSION)
	rm -rf "$(dir $@)"
	mkdir -p "$(dir $@)"
	touch $@
$(GIT_CHGLOG): $(GIT_CHGLOG_VERSION_FILE)

$(MOCKERY_VERSION_FILE): $(AP_DEVCACHE)
	@echo "installing mockery $(MOCKERY_VERSION) ..."
	rm -f $(MOCKERY)
	GOBIN=$(AP_DEVCACHE_BIN) go install -ldflags '-s -w -X $(MOCKERY_PACKAGE_NAME)/pkg/config.SemVer=$(MOCKERY_VERSION)' $(MOCKERY_PACKAGE_NAME)@$(MOCKERY_VERSION)
	rm -rf "$(dir $@)"
	mkdir -p "$(dir $@)"
	touch $@
$(MOCKERY): $(MOCKERY_VERSION_FILE)

$(GOLANGCI_LINT_VERSION_FILE): $(AP_DEVCACHE)
	@echo "installing golangci-lint $(GOLANGCI_LINT_VERSION) ..."
	rm -f $(MOCKERY)
	GOBIN=$(AP_DEVCACHE_BIN) go install github.com/golangci/golangci-lint/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	rm -rf "$(dir $@)"
	mkdir -p "$(dir $@)"
	touch $@
$(GOLANGCI_LINT): $(GOLANGCI_LINT_VERSION_FILE)

$(K8S_CODEGEN_VERSION_FILE): $(AP_DEVCACHE)
	@echo "installing k8s code-generator $(K8S_CODEGEN_VERSION) ..."
	rm -f $(K8S_GO_TO_PROTOBUF)
	GOBIN=$(AP_DEVCACHE_BIN) go install k8s.io/code-generator/...
	wget -q https://raw.githubusercontent.com/kubernetes/code-generator/$(K8S_CODEGEN_VERSION)/$(K8S_KUBE_CODEGEN_FILE) -O $(K8S_KUBE_CODEGEN)
	rm -rf "$(dir $@)"
	mkdir -p "$(dir $@)"
	touch $@
	chmod +x $(K8S_KUBE_CODEGEN)
$(K8S_KUBE_CODEGEN): $(K8S_CODEGEN_VERSION_FILE)
$(K8S_GO_TO_PROTOBUF): $(K8S_CODEGEN_VERSION_FILE)

ifeq (false, $(_SYSTEM_KIND))
$(KIND_VERSION_FILE): $(AP_DEVCACHE)
	@echo "installing kind $(KIND_VERSION) ..."
	GOBIN=$(AP_DEVCACHE_BIN) go install sigs.k8s.io/kind@$(KIND_VERSION)
	rm -rf "$(dir $@)"
	mkdir -p "$(dir $@)"
	touch $@
$(KIND): $(KIND_VERSION_FILE)
else
	@echo "using alread installed kind $(KIND_VERSION) $(KIND)"
endif

cache-clean:
	rm -rf $(AP_DEVCACHE)
