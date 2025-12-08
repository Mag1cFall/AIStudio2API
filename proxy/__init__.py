import asyncio

from proxy import runner


def start(*args, **kwargs):
    if args:
        queue = args[0] if len(args) > 0 else None
        port = args[1] if len(args) > 1 else None
        proxy_url = args[2] if len(args) > 2 else None
    else:
        queue = kwargs.get('queue', None)
        port = kwargs.get('port', None)
        proxy_url = kwargs.get('proxy', None)
    asyncio.run(runner.embedded(queue=queue, port=port, proxy_url=proxy_url))