class ClientDisconnectedError(Exception):
    """客户端断开连接异常"""
    pass 
class ElementClickError(Exception):
    """当所有点击元素的尝试都失败时引发"""
    pass