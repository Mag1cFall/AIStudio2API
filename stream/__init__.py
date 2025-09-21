import asyncio
import multiprocessing
from stream import main

def start(*args, **kwargs):
    if args:
        queue = args[0] if len(args) > 0 else None
        port = args[1] if len(args) > 1 else None
        proxy = args[2] if len(args) > 2 else None
    else:
        queue = kwargs.get('queue', None)
        port = kwargs.get('port', None)
        proxy = kwargs.get('proxy', None)
    asyncio.run(main.builtin(queue=queue, port=port, proxy=proxy))