# Nutanix Telemetry Classification Fix

## Overview

This release addresses a critical telemetry issue where Nutanix OpenShift clusters were inconsistently classified in telemetry data, preventing accurate business reporting and cluster counting.

## Problem Statement

### Issue
Nutanix OpenShift clusters showed inconsistent `host_type` values in telemetry data:
- Sometimes: `host_type="kvm-unknown"`
- Sometimes: `host_type="virt-unknown"`
- Sometimes: `host_type="nutanix_ahv"`

### Business Impact
- **LDTN Reports**: Inaccurate Nutanix cluster counts
- **TeleSense Data**: Unreliable virtualization platform analytics
- **Business Decisions**: Lack of visibility into Nutanix adoption

### Root Cause
When `virt-what` detects multiple virtualization technologies on Nutanix nodes (e.g., both `kvm` and `nutanix_ahv`), the PromQL `topk` function randomly selects between equal-valued metrics, leading to inconsistent classification.

## Solution

### New Telemetry Rule: `id_primary_host_product`

We've introduced a new recording rule that provides reliable virtualization **product** classification, separate from underlying **technology** detection.

**Key Features:**
- Always classifies Nutanix clusters as `host_product="nutanix"`
- Eliminates random selection between competing technologies
- Provides clean product-level classification for business reporting

### Backwards Compatibility

The existing `id_primary_host_type` rule remains unchanged to maintain full backwards compatibility. Teams can migrate to the new rule at their own pace.

## Usage Examples

### Before (Complex Workaround)
```sql
SELECT cluster_id, host_type, provider
FROM telemetry
WHERE (host_type = 'kvm-unknown' AND provider = 'Nutanix')
   OR (host_type = 'virt-unknown' AND provider = 'Nutanix')
   OR (host_type = 'nutanix_ahv' AND provider = 'Nutanix')
```

### After (Simple & Reliable)
```sql
SELECT cluster_id, host_product
FROM telemetry
WHERE host_product = 'nutanix'
```

## Business Use Cases

### LDTN Reports
```sql
-- Accurate Nutanix cluster counting
SELECT COUNT(*) as nutanix_clusters
FROM telemetry
WHERE host_product = 'nutanix'
```

### TeleSense Analytics
```sql
-- Reliable virtualization platform distribution
SELECT host_product, COUNT(*) as cluster_count
FROM telemetry
GROUP BY host_product
ORDER BY cluster_count DESC
```

### Product Metrics Dashboard
```sql
-- Monthly Nutanix adoption trend
SELECT DATE_TRUNC('month', timestamp) as month,
       COUNT(DISTINCT cluster_id) as nutanix_clusters
FROM telemetry
WHERE host_product = 'nutanix'
GROUP BY month
ORDER BY month
```

## Platform Classification Mapping

| Input (virt-what) | host\_product | Description |
|------------------|--------------|-------------|
| `nutanix_ahv` | `nutanix` | Nutanix AHV virtualization |
| `aws` | `aws` | Amazon Web Services |
| `vmware` | `vmware` | VMware vSphere |
| `gcp` | `gcp` | Google Cloud Platform |
| `openstack` | `openstack` | OpenStack |
| `hyperv` | `hyperv` | Microsoft Hyper-V |
| `ovirt` | `ovirt` | Red Hat Virtualization |
| `none` | `metal` | Bare metal installation |
| `kvm` | `unknown` | Technology, not product |

## Migration Guide

### Query Migration Examples

#### Nutanix Cluster Detection
```sql
-- Old approach (unreliable)
WHERE host_type IN ('nutanix_ahv', 'kvm-unknown') AND provider = 'Nutanix'

-- New approach (reliable)
WHERE host_product = 'nutanix'
```

#### Multi-Platform Analysis
```sql
-- Old approach (technology-focused)
WHERE host_type IN ('aws', 'vmware', 'openstack')

-- New approach (product-focused)
WHERE host_product IN ('aws', 'vmware', 'openstack')
```

## Technical Details

### Implementation
- **File**: `jsonnet/telemeter/rules.libsonnet`
- **Rule Name**: `id_primary_host_product`
- **Interval**: 4 minutes
- **Dependencies**: `cluster:virt_platform_nodes:sum`, `cluster_version`

### Testing
All major virtualization platforms tested and verified:
- ✅ Nutanix AHV → `host_product="nutanix"`
- ✅ AWS → `host_product="aws"`
- ✅ VMware → `host_product="vmware"`
- ✅ Bare Metal → `host_product="metal"`

## Benefits

### For Business Teams
- **Accurate Reporting**: Reliable Nutanix cluster counts
- **Simplified Queries**: No more complex multi-condition logic
- **Consistent Data**: Eliminates random classification variations

### For Engineering Teams
- **Backwards Compatible**: Existing queries continue to work
- **Clean Architecture**: Separates product from technology concerns
- **Future-Ready**: Foundation for additional product classifications

## Future Enhancements

A planned `id_primary_host_technology` rule will complement this fix by providing reliable underlying technology detection (KVM, Xen, etc.) while maintaining the clean product classification introduced here.
