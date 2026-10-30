INSERT INTO topology_links(primary_id,standby_id,application_name,provenance) VALUES($1,$2,$3,$4) ON CONFLICT(standby_id) DO UPDATE SET primary_id=$1,application_name=$3,provenance=$4
