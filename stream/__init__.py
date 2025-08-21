import asyncio
import multiprocessing

from stream import main

def start(*args, **kwargs):
    """
    启动流式代理服务器，兼容位置参数和关键字参数

    位置参数模式（与参考文件兼容）：
        start(queue, port, proxy)

    关键字参数模式：
        start(queue=queue, port=port, proxy=proxy)
    """
    if args:
        # 位置参数模式（与参考文件兼容）
        queue = args[0] if len(args) > 0 else None
        port = args[1] if len(args) > 1 else None
        proxy = args[2] if len(args) > 2 else None
    else:
        # 关键字参数模式
        queue = kwargs.get('queue', None)
        port = kwargs.get('port', None)
        proxy = kwargs.get('proxy', None)

    asyncio.run(main.builtin(queue=queue, port=port, proxy=proxy))