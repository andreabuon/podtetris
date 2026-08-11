.PHONY: cluster clean

cluster:
	cd ../labs/lab6_kwok+kind_cost && ./setup-cluster.sh

clean:
	-$(MAKE) -C webhook undeploy
	-$(MAKE) -C evictor undeploy ignore-not-found=true
	-$(MAKE) -C planner clean
#	kind delete cluster "kind"