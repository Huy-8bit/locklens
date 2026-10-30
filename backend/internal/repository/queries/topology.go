package queries

import _ "embed"

//go:embed topology/topology_links.sql
var TopologyLinks string

//go:embed topology/set_topology_link.sql
var SetTopologyLink string

//go:embed topology/delete_topology_link.sql
var DeleteTopologyLink string
