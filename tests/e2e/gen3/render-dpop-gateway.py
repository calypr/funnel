"""Add a real DPoP gateway beside the bearer gateway in the Kind fixture."""
import copy
import json
import sys
from pathlib import Path

resources = [
    json.loads(line)
    for line in Path(sys.argv[1]).read_text().splitlines()
    if line.strip()
]
config = json.loads(Path(sys.argv[2]).read_text())

def find(kind, name):
    matches = [
        resource for resource in resources
        if resource and resource.get('kind') == kind
        and resource['metadata']['name'] == name
    ]
    if len(matches) != 1:
        raise ValueError(f'expected one {kind}/{name}, found {len(matches)}')
    return matches[0]

namespace = find('Deployment', 'funnel-server')['metadata']['namespace']
endpoint = f'http://revproxy-service.{namespace}.svc.cluster.local'
config.update(
    DPOP_REQUIRED=True,
    DPOP_ALLOWED_ISSUERS=[endpoint + '/user'],
    DPOP_EXTERNAL_BASE_URL=endpoint,
)
secret = copy.deepcopy(find('Secret', 'gen3workflow-g3auto'))
secret['metadata']['name'] = 'gen3workflow-dpop-config'
secret['stringData']['gen3-workflow-config.yaml'] = json.dumps(config)
deployment = copy.deepcopy(find('Deployment', 'gen3-workflow-deployment'))
deployment['metadata']['name'] = 'gen3-workflow-dpop-deployment'
deployment['spec']['selector']['matchLabels']['app'] = 'gen3-workflow-dpop'
deployment['spec']['template']['metadata']['labels']['app'] = 'gen3-workflow-dpop'
for volume in deployment['spec']['template']['spec']['volumes']:
    if volume.get('secret', {}).get('secretName') == 'gen3workflow-g3auto':
        volume['secret']['secretName'] = 'gen3workflow-dpop-config'
service = copy.deepcopy(find('Service', 'gen3-workflow-service'))
service['metadata']['name'] = 'gen3-workflow-dpop-service'
service['spec']['selector']['app'] = 'gen3-workflow-dpop'
proxy = find('ConfigMap', 'revproxy-nginx-subconf')
key = 'gen3-workflow-service.conf'
original = 'http://gen3-workflow-service.$namespace.svc.cluster.local'
if proxy['data'][key].count(original) != 2:
    raise ValueError('unexpected TES and workflows proxy routes')
proxy['data'][key] = proxy['data'][key].replace(
    original, 'http://gen3-workflow-dpop-service.$namespace.svc.cluster.local', 1
)
resources.extend([secret, deployment, service])
# The gateway retains the chart's public/netnolimit labels and its real auth
# dependencies. Funnel's ingress selector must also admit the second gateway.
resources.append({
    'apiVersion': 'networking.k8s.io/v1', 'kind': 'NetworkPolicy',
    'metadata': {'name': 'funnel-from-dpop-gateway', 'namespace': namespace},
    'spec': {'podSelector': {'matchLabels': {'app': 'funnel'}}, 'policyTypes': ['Ingress'],
             'ingress': [{'from': [{'podSelector': {'matchLabels': {'app': 'gen3-workflow-dpop'}}}],
                          'ports': [{'protocol': 'TCP', 'port': 8000}]}]}})
for resource in resources:
    if resource:
        print(json.dumps(resource))
        print('---')
