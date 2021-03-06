// Copyright (c) 2016 Intel Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"testing"
	"time"

	yaml "gopkg.in/yaml.v2"

	"github.com/ciao-project/ciao/ciao-controller/api"
	"github.com/ciao-project/ciao/ciao-controller/types"
	"github.com/ciao-project/ciao/payloads"
	"github.com/ciao-project/ciao/ssntp"
	"github.com/ciao-project/ciao/testutil"
)

// ByTenantID is used to sort CNCI instances by Tenant ID.
type ByTenantID []types.CiaoCNCI

func (c ByTenantID) Len() int {
	return len(c)
}

func (c ByTenantID) Swap(i int, j int) {
	c[i], c[j] = c[j], c[i]
}

func (c ByTenantID) Less(i int, j int) bool {
	return c[i].TenantID < c[j].TenantID
}

func testHTTPRequest(t *testing.T, method string, URL string, expectedResponse int, data []byte, validToken bool) []byte {
	req, err := http.NewRequest(method, URL, bytes.NewBuffer(data))
	if err != nil {
		t.Fatal(err)
	}

	req.Header.Set("Content-Type", "application/json")

	tlsConfig := &tls.Config{}

	clientCertFile := "/etc/pki/ciao/auth-admin.pem"
	cert, err := tls.LoadX509KeyPair(clientCertFile, clientCertFile)
	if err != nil {
		t.Fatalf("Unable to load client certiticate: %s", err)
	}

	tlsConfig.Certificates = []tls.Certificate{cert}
	tlsConfig.BuildNameToCertificate()

	transport := &http.Transport{
		TLSClientConfig: tlsConfig,
	}

	client := &http.Client{Transport: transport}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != expectedResponse {
		var msg string

		body, err := ioutil.ReadAll(resp.Body)
		if err == nil {
			msg = string(body)
		}

		t.Fatalf("expected: %d, got: %d, msg: %s", expectedResponse, resp.StatusCode, msg)
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}

	return body
}

func testCreateServer(t *testing.T, n int) api.Servers {
	tenant, err := ctl.ds.GetTenant(testutil.ComputeUser)
	if err != nil {
		t.Fatal(err)
	}

	// get a valid workload ID
	wls, err := ctl.ds.GetWorkloads(tenant.ID)
	if err != nil {
		t.Fatal(err)
	}

	if len(wls) == 0 {
		t.Fatalf("No valid workloads for tenant: %s\n", tenant.ID)
	}

	url := testutil.ComputeURL + "/" + tenant.ID + "/instances"

	var server api.CreateServerRequest
	server.Server.MaxInstances = n
	server.Server.WorkloadID = wls[0].ID

	b, err := json.Marshal(server)
	if err != nil {
		t.Fatal(err)
	}

	body := testHTTPRequest(t, "POST", url, http.StatusAccepted, b, true)

	servers := api.Servers{}

	err = json.Unmarshal(body, &servers)
	if err != nil {
		t.Fatal(err)
	}

	if servers.TotalServers != n {
		t.Fatal("Not enough servers returned")
	}

	return servers
}

func testListServerDetailsTenant(t *testing.T, tenantID string) api.Servers {
	url := testutil.ComputeURL + "/" + tenantID + "/instances/detail"

	body := testHTTPRequest(t, "GET", url, http.StatusOK, nil, true)

	s := api.Servers{}
	err := json.Unmarshal(body, &s)
	if err != nil {
		t.Fatal(err)
	}

	return s
}

func TestCreateSingleServer(t *testing.T) {
	_ = testCreateServer(t, 1)
}

func TestListServerDetailsTenant(t *testing.T) {
	tenant, err := ctl.ds.GetTenant(testutil.ComputeUser)
	if err != nil {
		t.Fatal(err)
	}

	servers := testCreateServer(t, 1)
	if servers.TotalServers != 1 {
		t.Fatal(err)
	}

	s := testListServerDetailsTenant(t, tenant.ID)

	if s.TotalServers < 1 {
		t.Fatal("Not enough servers returned")
	}
}

func testShowServerDetails(t *testing.T, httpExpectedStatus int, validToken bool) {
	tenant, err := ctl.ds.GetTenant(testutil.ComputeUser)
	if err != nil {
		t.Fatal(err)
	}

	tURL := testutil.ComputeURL + "/" + tenant.ID + "/instances/"

	servers := testCreateServer(t, 1)
	if servers.TotalServers != 1 {
		t.Fatal(err)
	}

	s := testListServerDetailsTenant(t, tenant.ID)

	if s.TotalServers < 1 {
		t.Fatal("Not enough servers returned")
	}

	for _, s1 := range s.Servers {
		url := tURL + s1.ID

		body := testHTTPRequest(t, "GET", url, httpExpectedStatus, nil, validToken)
		// stop evaluating in case the scenario is InvalidToken
		if httpExpectedStatus == 401 {
			return
		}

		var s2 api.Server
		err = json.Unmarshal(body, &s2)
		if err != nil {
			t.Fatal(err)
		}

		if reflect.DeepEqual(s1, s2.Server) == false {
			t.Fatal("Server details not correct")
			//t.Fatalf("Server details not correct %s %s", s1, s2.Server)
		}
	}
}

func TestShowServerDetails(t *testing.T) {
	testShowServerDetails(t, http.StatusOK, true)
}

func testDeleteServer(t *testing.T, httpExpectedStatus int, httpExpectedErrorStatus int, validToken bool) {
	tenant, err := ctl.ds.GetTenant(testutil.ComputeUser)
	if err != nil {
		t.Fatal(err)
	}

	// instances have to be assigned to a node to be deleted
	client, err := testutil.NewSsntpTestClientConnection("DeleteServer", ssntp.AGENT, testutil.AgentUUID)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Shutdown()

	tURL := testutil.ComputeURL + "/" + tenant.ID + "/instances/"

	servers := testCreateServer(t, 10)
	if servers.TotalServers != 10 {
		t.Fatal(err)
	}

	time.Sleep(2 * time.Second)

	sendStatsCmd(client, t)

	time.Sleep(2 * time.Second)

	s := testListServerDetailsTenant(t, tenant.ID)

	if s.TotalServers < 1 {
		t.Fatal("Not enough servers returned")
	}

	for _, s1 := range s.Servers {
		url := tURL + s1.ID
		if s1.NodeID != "" {
			_ = testHTTPRequest(t, "DELETE", url, httpExpectedStatus, nil, validToken)
		} else {
			_ = testHTTPRequest(t, "DELETE", url, httpExpectedErrorStatus, nil, validToken)
		}
	}
}

func TestDeleteServer(t *testing.T) {
	testDeleteServer(t, http.StatusNoContent, http.StatusForbidden, true)
}

func testServersActionStart(t *testing.T, httpExpectedStatus int, validToken bool) {
	tenant, err := ctl.ds.GetTenant(testutil.ComputeUser)
	if err != nil {
		t.Fatal(err)
	}

	url := testutil.ComputeURL + "/v2.1/" + tenant.ID + "/servers/action"

	client, err := testutil.NewSsntpTestClientConnection("ServersActionStart", ssntp.AGENT, testutil.AgentUUID)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Shutdown()

	servers := testCreateServer(t, 1)
	if servers.TotalServers != 1 {
		t.Fatal(err)
	}

	time.Sleep(2 * time.Second)

	sendStatsCmd(client, t)

	time.Sleep(1 * time.Second)

	err = ctl.stopInstance(servers.Servers[0].ID)
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(1 * time.Second)

	sendStatsCmd(client, t)

	time.Sleep(1 * time.Second)

	var ids []string
	ids = append(ids, servers.Servers[0].ID)

	cmd := types.CiaoServersAction{
		Action:    "os-start",
		ServerIDs: ids,
	}

	b, err := json.Marshal(cmd)
	if err != nil {
		t.Fatal(err)
	}

	_ = testHTTPRequest(t, "POST", url, httpExpectedStatus, b, validToken)
}

func TestServersActionStart(t *testing.T) {
	testServersActionStart(t, http.StatusAccepted, true)
}

func testServersActionStop(t *testing.T, httpExpectedStatus int, action string) {
	tenant, err := ctl.ds.GetTenant(testutil.ComputeUser)
	if err != nil {
		t.Fatal(err)
	}

	url := testutil.ComputeURL + "/v2.1/" + tenant.ID + "/servers/action"

	client, err := testutil.NewSsntpTestClientConnection("ServersActionStop", ssntp.AGENT, testutil.AgentUUID)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Shutdown()

	servers := testCreateServer(t, 1)
	if servers.TotalServers != 1 {
		t.Fatal(err)
	}

	time.Sleep(2 * time.Second)

	sendStatsCmd(client, t)

	time.Sleep(1 * time.Second)

	var ids []string
	ids = append(ids, servers.Servers[0].ID)

	cmd := types.CiaoServersAction{
		Action:    action,
		ServerIDs: ids,
	}

	b, err := json.Marshal(cmd)
	if err != nil {
		t.Fatal(err)
	}

	_ = testHTTPRequest(t, "POST", url, httpExpectedStatus, b, true)
}

func TestServersActionStop(t *testing.T) {
	testServersActionStop(t, http.StatusAccepted, "os-stop")
}

func TestServersActionStopWrongAction(t *testing.T) {
	testServersActionStop(t, http.StatusServiceUnavailable, "wrong-action")
}

func testServerActionStop(t *testing.T, httpExpectedStatus int, validToken bool) {
	action := "os-stop"

	tenant, err := ctl.ds.GetTenant(testutil.ComputeUser)
	if err != nil {
		t.Fatal(err)
	}

	client, err := testutil.NewSsntpTestClientConnection("ServerActionStop", ssntp.AGENT, testutil.AgentUUID)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Shutdown()

	servers := testCreateServer(t, 1)
	if servers.TotalServers != 1 {
		t.Fatal(err)
	}

	time.Sleep(2 * time.Second)

	sendStatsCmd(client, t)

	time.Sleep(1 * time.Second)

	url := testutil.ComputeURL + "/" + tenant.ID + "/instances/" + servers.Servers[0].ID + "/action"
	_ = testHTTPRequest(t, "POST", url, httpExpectedStatus, []byte(action), validToken)
}

func TestServerActionStop(t *testing.T) {
	testServerActionStop(t, http.StatusAccepted, true)
}

func TestServerActionStart(t *testing.T) {
	action := "os-start"

	tenant, err := ctl.ds.GetTenant(testutil.ComputeUser)
	if err != nil {
		t.Fatal(err)
	}

	client, err := testutil.NewSsntpTestClientConnection("ServerActionStart", ssntp.AGENT, testutil.AgentUUID)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Shutdown()

	servers := testCreateServer(t, 1)
	if servers.TotalServers != 1 {
		t.Fatal(err)
	}

	time.Sleep(1 * time.Second)

	sendStatsCmd(client, t)

	time.Sleep(1 * time.Second)

	serverCh := server.AddCmdChan(ssntp.DELETE)

	err = ctl.stopInstance(servers.Servers[0].ID)
	if err != nil {
		t.Fatal(err)
	}

	err = sendStopEvent(client, servers.Servers[0].ID)
	if err != nil {
		t.Fatal(err)
	}

	_, err = server.GetCmdChanResult(serverCh, ssntp.DELETE)
	if err != nil {
		t.Fatal(err)
	}

	url := testutil.ComputeURL + "/" + tenant.ID + "/instances/" + servers.Servers[0].ID + "/action"
	_ = testHTTPRequest(t, "POST", url, http.StatusAccepted, []byte(action), true)
}

func sendStopEvent(client *testutil.SsntpTestClient, instanceUUID string) error {
	event := payloads.EventInstanceStopped{
		InstanceStopped: payloads.InstanceStoppedEvent{
			InstanceUUID: instanceUUID,
		},
	}
	y, err := yaml.Marshal(event)
	if err != nil {
		return fmt.Errorf("Unable to create InstanceStopped payload : %v", err)
	}
	clientEvtCh := wrappedClient.addEventChan(ssntp.InstanceStopped)
	client.Ssntp.SendEvent(ssntp.InstanceStopped, y)
	err = wrappedClient.getEventChan(clientEvtCh, ssntp.InstanceStopped)
	if err != nil {
		return fmt.Errorf("InstanceStopped event not received: %v", err)
	}

	return nil
}

func testListTenantResources(t *testing.T, httpExpectedStatus int, validToken bool) {
	var usage types.CiaoUsageHistory

	endTime := time.Now()
	startTime := endTime.Add(-15 * time.Minute)

	tenant, err := ctl.ds.GetTenant(testutil.ComputeUser)
	if err != nil {
		t.Fatal(err)
	}

	tURL := testutil.ComputeURL + "/v2.1/" + tenant.ID + "/resources?"

	usage.Usages, err = ctl.ds.GetTenantUsage(tenant.ID, startTime, endTime)
	if err != nil {
		t.Fatal(err)
	}

	v := url.Values{}
	v.Add("start_date", startTime.Format(time.RFC3339))
	v.Add("end_date", endTime.Format(time.RFC3339))

	tURL += v.Encode()

	body := testHTTPRequest(t, "GET", tURL, httpExpectedStatus, nil, validToken)
	// stop evaluating in case the scenario is InvalidToken
	if httpExpectedStatus == 401 {
		return
	}

	var result types.CiaoUsageHistory

	err = json.Unmarshal(body, &result)
	if err != nil {
		t.Fatal(err)
	}

	if reflect.DeepEqual(usage, result) == false {
		t.Fatal("Tenant usage not correct")
	}
}

func TestListTenantResources(t *testing.T) {
	testListTenantResources(t, http.StatusOK, true)
}

func testListTenantQuotas(t *testing.T, httpExpectedStatus int, validToken bool) {
	tenant, err := ctl.ds.GetTenant(testutil.ComputeUser)
	if err != nil {
		t.Fatal(err)
	}

	url := testutil.ComputeURL + "/v2.1/" + tenant.ID + "/quotas"

	var expected types.CiaoTenantResources

	qds := ctl.qs.DumpQuotas(tenant.ID)

	qd := findQuota(qds, "tenant-instances-quota")
	if qd != nil {
		expected.InstanceLimit = qd.Value
		expected.InstanceUsage = qd.Usage
	}
	qd = findQuota(qds, "tenant-vcpu-quota")
	if qd != nil {
		expected.VCPULimit = qd.Value
		expected.VCPUUsage = qd.Usage
	}
	qd = findQuota(qds, "tenant-mem-quota")
	if qd != nil {
		expected.MemLimit = qd.Value
		expected.MemUsage = qd.Usage
	}
	qd = findQuota(qds, "tenant-storage-quota")
	if qd != nil {
		expected.DiskLimit = qd.Value
		expected.DiskUsage = qd.Usage
	}

	expected.ID = tenant.ID

	body := testHTTPRequest(t, "GET", url, httpExpectedStatus, nil, validToken)
	// stop evaluating in case the scenario is InvalidToken
	if httpExpectedStatus == 401 {
		return
	}

	var result types.CiaoTenantResources

	err = json.Unmarshal(body, &result)
	if err != nil {
		t.Fatal(err)
	}

	expected.Timestamp = result.Timestamp

	if reflect.DeepEqual(expected, result) == false {
		t.Fatal("Tenant quotas not correct")
	}
}

func TestListTenantQuotas(t *testing.T) {
	testListTenantQuotas(t, http.StatusOK, true)
}

func testListEventsTenant(t *testing.T, httpExpectedStatus int, validToken bool) {
	tenant, err := ctl.ds.GetTenant(testutil.ComputeUser)
	if err != nil {
		t.Fatal(err)
	}

	url := testutil.ComputeURL + "/v2.1/" + tenant.ID + "/events"

	expected := types.NewCiaoEvents()

	logs, err := ctl.ds.GetEventLog()
	if err != nil {
		t.Fatal(err)
	}

	for _, l := range logs {
		if tenant.ID != l.TenantID {
			continue
		}

		event := types.CiaoEvent{
			Timestamp: l.Timestamp,
			TenantID:  l.TenantID,
			EventType: l.EventType,
			Message:   l.Message,
		}
		expected.Events = append(expected.Events, event)
	}

	body := testHTTPRequest(t, "GET", url, httpExpectedStatus, nil, validToken)

	var result types.CiaoEvents

	err = json.Unmarshal(body, &result)
	if err != nil {
		t.Fatal(err)
	}

	if reflect.DeepEqual(expected, result) == false {
		t.Fatal("Tenant events not correct")
	}
}

func TestListEventsTenant(t *testing.T) {
	testListEventsTenant(t, http.StatusOK, true)
}

func testListNodeServers(t *testing.T, httpExpectedStatus int, validToken bool) {
	computeNodes := ctl.ds.GetNodeLastStats()

	for _, n := range computeNodes.Nodes {
		instances, err := ctl.ds.GetAllInstancesByNode(n.ID)
		if err != nil {
			t.Fatal(err)
		}

		url := testutil.ComputeURL + "/v2.1/nodes/" + n.ID + "/servers/detail"

		body := testHTTPRequest(t, "GET", url, httpExpectedStatus, nil, validToken)
		// stop evaluating in case the scenario is InvalidToken
		if httpExpectedStatus == 401 {
			return
		}

		var result types.CiaoServersStats

		err = json.Unmarshal(body, &result)
		if err != nil {
			t.Fatal(err)
		}

		if result.TotalServers != len(instances) {
			t.Fatal("Incorrect number of servers")
		}

		// TBD: make sure result exactly matches expected results.
		// this isn't done now because the list of instances is
		// possibly out of order
	}
}

func TestListNodeServers(t *testing.T) {
	testListNodeServers(t, http.StatusOK, true)
}

func testListNodes(t *testing.T, httpExpectedStatus int, validToken bool) {
	expected := ctl.ds.GetNodeLastStats()

	summary, err := ctl.ds.GetNodeSummary()
	if err != nil {
		t.Fatal(err)
	}

	for _, node := range summary {
		for i := range expected.Nodes {
			if expected.Nodes[i].ID != node.NodeID {
				continue
			}

			expected.Nodes[i].TotalInstances = node.TotalInstances
			expected.Nodes[i].TotalRunningInstances = node.TotalRunningInstances
			expected.Nodes[i].TotalPendingInstances = node.TotalPendingInstances
			expected.Nodes[i].TotalPausedInstances = node.TotalPausedInstances
			expected.Nodes[i].Timestamp = time.Time{}
		}
	}

	sort.Sort(types.SortedNodesByID(expected.Nodes))

	url := testutil.ComputeURL + "/v2.1/nodes"

	body := testHTTPRequest(t, "GET", url, httpExpectedStatus, nil, validToken)
	// stop evaluating in case the scenario is InvalidToken
	if httpExpectedStatus == 401 {
		return
	}

	var result types.CiaoNodes

	err = json.Unmarshal(body, &result)
	if err != nil {
		t.Fatal(err)
	}

	for i := range result.Nodes {
		result.Nodes[i].Timestamp = time.Time{}
	}

	if reflect.DeepEqual(expected.Nodes, result.Nodes) == false {
		t.Fatalf("expected: \n%+v\n result: \n%+v\n", expected, result)
	}
}

func TestListNodes(t *testing.T) {
	testListNodes(t, http.StatusOK, true)
}

func testNodeSummary(t *testing.T, httpExpectedStatus int, validToken bool) {
	var expected types.CiaoClusterStatus

	computeNodes := ctl.ds.GetNodeLastStats()

	expected.Status.TotalNodes = len(computeNodes.Nodes)
	for _, node := range computeNodes.Nodes {
		if node.Status == ssntp.READY.String() {
			expected.Status.TotalNodesReady++
		} else if node.Status == ssntp.FULL.String() {
			expected.Status.TotalNodesFull++
		} else if node.Status == ssntp.OFFLINE.String() {
			expected.Status.TotalNodesOffline++
		} else if node.Status == ssntp.MAINTENANCE.String() {
			expected.Status.TotalNodesMaintenance++
		}
	}

	url := testutil.ComputeURL + "/v2.1/nodes/summary"

	body := testHTTPRequest(t, "GET", url, httpExpectedStatus, nil, validToken)
	// stop evaluating in case the scenario is InvalidToken
	if httpExpectedStatus == 401 {
		return
	}

	var result types.CiaoClusterStatus

	err := json.Unmarshal(body, &result)
	if err != nil {
		t.Fatal(err)
	}

	if reflect.DeepEqual(expected, result) == false {
		t.Fatalf("expected: \n%+v\n result: \n%+v\n", expected, result)
	}
}

func TestNodeSummary(t *testing.T) {
	testNodeSummary(t, http.StatusOK, true)
}

func testListCNCIs(t *testing.T, httpExpectedStatus int, validToken bool) {
	var expected types.CiaoCNCIs

	cncis, err := ctl.ds.GetTenantCNCISummary("")
	if err != nil {
		t.Fatal(err)
	}

	for _, cnci := range cncis {
		var subnets []types.CiaoCNCISubnet

		if cnci.InstanceID == "" {
			continue
		}

		for _, subnet := range cnci.Subnets {
			subnets = append(subnets,
				types.CiaoCNCISubnet{
					Subnet: subnet,
				},
			)
		}

		expected.CNCIs = append(expected.CNCIs,
			types.CiaoCNCI{
				ID:       cnci.InstanceID,
				TenantID: cnci.TenantID,
				IPv4:     cnci.IPAddress,
				Subnets:  subnets,
			},
		)
	}

	sort.Sort(ByTenantID(expected.CNCIs))

	url := testutil.ComputeURL + "/v2.1/cncis"

	body := testHTTPRequest(t, "GET", url, httpExpectedStatus, nil, validToken)
	// stop evaluating in case the scenario is InvalidToken
	if httpExpectedStatus == 401 {
		return
	}

	var result types.CiaoCNCIs

	err = json.Unmarshal(body, &result)
	if err != nil {
		t.Fatal(err)
	}

	sort.Sort(ByTenantID(result.CNCIs))

	if reflect.DeepEqual(expected, result) == false {
		t.Fatalf("expected: \n%+v\n result: \n%+v\n", expected, result)
	}
}

func TestListCNCIs(t *testing.T) {
	testListCNCIs(t, http.StatusOK, true)
}

func testListCNCIDetails(t *testing.T, httpExpectedStatus int, validToken bool) {
	cncis, err := ctl.ds.GetTenantCNCISummary("")
	if err != nil {
		t.Fatal(err)
	}

	for _, cnci := range cncis {
		var expected types.CiaoCNCI

		cncis, err := ctl.ds.GetTenantCNCISummary(cnci.InstanceID)
		if err != nil {
			t.Fatal(err)
		}

		if len(cncis) > 0 {
			var subnets []types.CiaoCNCISubnet
			cnci := cncis[0]

			for _, subnet := range cnci.Subnets {
				subnets = append(subnets,
					types.CiaoCNCISubnet{
						Subnet: subnet,
					},
				)
			}

			expected = types.CiaoCNCI{
				ID:       cnci.InstanceID,
				TenantID: cnci.TenantID,
				IPv4:     cnci.IPAddress,
				Subnets:  subnets,
			}
		}

		url := testutil.ComputeURL + "/v2.1/cncis/" + cnci.InstanceID + "/detail"

		body := testHTTPRequest(t, "GET", url, httpExpectedStatus, nil, validToken)
		// stop evaluating in case the scenario is InvalidToken
		if httpExpectedStatus == 401 {
			return
		}

		var result types.CiaoCNCI

		err = json.Unmarshal(body, &result)
		if err != nil {
			t.Fatal(err)
		}

		if reflect.DeepEqual(expected, result) == false {
			t.Fatalf("expected: \n%+v\n result: \n%+v\n", expected, result)
		}
	}
}

func TestListCNCIDetails(t *testing.T) {
	testListCNCIDetails(t, http.StatusOK, true)
}

func testListTraces(t *testing.T, httpExpectedStatus int, validToken bool) {
	var expected types.CiaoTracesSummary

	client := testStartTracedWorkload(t)
	defer client.Shutdown()

	sendTraceReportEvent(client, t)

	time.Sleep(2 * time.Second)

	summaries, err := ctl.ds.GetBatchFrameSummary()
	if err != nil {
		t.Fatal(err)
	}

	for _, s := range summaries {
		summary := types.CiaoTraceSummary{
			Label:     s.BatchID,
			Instances: s.NumInstances,
		}
		expected.Summaries = append(expected.Summaries, summary)
	}

	url := testutil.ComputeURL + "/v2.1/traces"

	body := testHTTPRequest(t, "GET", url, httpExpectedStatus, nil, validToken)
	// stop evaluating in case the scenario is InvalidToken
	if httpExpectedStatus == 401 {
		return
	}

	var result types.CiaoTracesSummary

	err = json.Unmarshal(body, &result)
	if err != nil {
		t.Fatal(err)
	}

	if reflect.DeepEqual(expected, result) == false {
		t.Fatalf("expected: \n%+v\n result: \n%+v\n", expected, result)
	}
}

func TestListTraces(t *testing.T) {
	testListTraces(t, http.StatusOK, true)
}

func testListEvents(t *testing.T, httpExpectedStatus int, validToken bool) {
	url := testutil.ComputeURL + "/v2.1/events"

	expected := types.NewCiaoEvents()

	logs, err := ctl.ds.GetEventLog()
	if err != nil {
		t.Fatal(err)
	}

	for _, l := range logs {
		event := types.CiaoEvent{
			Timestamp: l.Timestamp,
			TenantID:  l.TenantID,
			EventType: l.EventType,
			Message:   l.Message,
		}
		expected.Events = append(expected.Events, event)
	}

	body := testHTTPRequest(t, "GET", url, httpExpectedStatus, nil, validToken)
	// stop evaluating in case the scenario is InvalidToken
	if httpExpectedStatus == 401 {
		return
	}

	var result types.CiaoEvents

	err = json.Unmarshal(body, &result)
	if err != nil {
		t.Fatal(err)
	}

	if reflect.DeepEqual(expected, result) == false {
		t.Fatalf("expected: \n%+v\n result: \n%+v\n", expected, result)
	}
}

func TestListEvents(t *testing.T) {
	testListEvents(t, http.StatusOK, true)
}

func testClearEvents(t *testing.T, httpExpectedStatus int, validToken bool) {
	url := testutil.ComputeURL + "/v2.1/events"

	_ = testHTTPRequest(t, "DELETE", url, httpExpectedStatus, nil, validToken)
	// stop evaluating in case the scenario is InvalidToken
	if httpExpectedStatus == 401 {
		return
	}

	logs, err := ctl.ds.GetEventLog()
	if err != nil {
		t.Fatal(err)
	}

	if len(logs) != 0 {
		t.Fatal("Logs not cleared")
	}
}

func TestClearEvents(t *testing.T) {
	testClearEvents(t, http.StatusAccepted, true)
}

func testTraceData(t *testing.T, httpExpectedStatus int, validToken bool) {
	client := testStartTracedWorkload(t)
	defer client.Shutdown()

	sendTraceReportEvent(client, t)

	time.Sleep(2 * time.Second)

	summaries, err := ctl.ds.GetBatchFrameSummary()
	if err != nil {
		t.Fatal(err)
	}

	for _, s := range summaries {
		var expected types.CiaoTraceData

		batchStats, err := ctl.ds.GetBatchFrameStatistics(s.BatchID)
		if err != nil {
			t.Fatal(err)
		}

		expected.Summary = types.CiaoBatchFrameStat{
			NumInstances:             batchStats[0].NumInstances,
			TotalElapsed:             batchStats[0].TotalElapsed,
			AverageElapsed:           batchStats[0].AverageElapsed,
			AverageControllerElapsed: batchStats[0].AverageControllerElapsed,
			AverageLauncherElapsed:   batchStats[0].AverageLauncherElapsed,
			AverageSchedulerElapsed:  batchStats[0].AverageSchedulerElapsed,
			VarianceController:       batchStats[0].VarianceController,
			VarianceLauncher:         batchStats[0].VarianceLauncher,
			VarianceScheduler:        batchStats[0].VarianceScheduler,
		}

		url := testutil.ComputeURL + "/v2.1/traces/" + s.BatchID

		body := testHTTPRequest(t, "GET", url, httpExpectedStatus, nil, validToken)
		// stop evaluating in case the scenario is InvalidToken
		if httpExpectedStatus == 401 {
			return
		}

		var result types.CiaoTraceData

		err = json.Unmarshal(body, &result)
		if err != nil {
			t.Fatal(err)
		}

		if reflect.DeepEqual(expected, result) == false {
			t.Fatalf("expected: \n%+v\n result: \n%+v\n", expected, result)
		}
	}
}

func TestTraceData(t *testing.T) {
	testTraceData(t, http.StatusOK, true)
}
