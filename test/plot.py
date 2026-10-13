#!/usr/bin/env python3

import matplotlib.pyplot as plt
import json
import sys
import os


d = os.path.dirname(sys.argv[1])
name = os.path.basename(sys.argv[1])
if name.endswith('.json'):
    name = name[:-5]
else:
    sys.exit("expected a JSON file")

parts = name.split('_')
ns = parts[0]
nc = parts[1]
metric = parts[2].upper()

divisor = 1
label = metric
if metric == "MEM":
    divisor = 1024*1024
    label = label + " (MB)"
else:
    label = label + " (%)"

j = {}
with open(sys.argv[1]) as f:
    j = json.load(f)

min_x = -1
results = []
for r in j['data']['result']:
    result = {}
    x = []
    y = []
    for v in r['values']:
        x.append(v[0])
        y.append(float(v[1])/divisor)
    result['x'] = x
    result['y'] = y
    result['name'] = r['metric']['pod']
    results.append(result)
    if (min_x == -1) or (x[0] < min_x):
        min_x = x[0]

for r in results:
    r['x'][:] = [x - min_x for x in r['x']]
    for x in r['x']:
        x = x - min_x
    plt.plot(r['x'], r['y'], label=r['name'])

plt.legend(loc=2, ncol=2)

# Add titles
plt.title(f"{metric} Utilization for {nc} Clients and {ns} Replicas")
plt.xlabel("Time (s)")
plt.ylabel(label)

plt.savefig(f"{d}/{name}.pdf", format="pdf")
