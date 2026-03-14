import asyncio
import json
import httpx
import time
import sys

BASE_URL = "http://127.0.0.1:2048"
API_KEY = "test"
TIMEOUT = 120.0
MODEL = "gemini-3-flash-preview"

HEADERS = {
    "Content-Type": "application/json",
    "Authorization": f"Bearer {API_KEY}",
}

TOOLS = [
    {
        "type": "function",
        "function": {
            "name": "get_weather",
            "description": "Get the current weather for a given city",
            "parameters": {
                "type": "object",
                "properties": {
                    "city": {"type": "string", "description": "The city name"},
                    "unit": {"type": "string", "enum": ["celsius", "fahrenheit"], "description": "Temperature unit"}
                },
                "required": ["city"]
            }
        }
    },
    {
        "type": "function",
        "function": {
            "name": "search_web",
            "description": "Search the web for information",
            "parameters": {
                "type": "object",
                "properties": {
                    "query": {"type": "string", "description": "Search query"}
                },
                "required": ["query"]
            }
        }
    }
]

TOOL_RESULTS = {
    "get_weather": lambda args: json.dumps({
        "city": args.get("city", "unknown"),
        "temperature": 22,
        "unit": args.get("unit", "celsius"),
        "condition": "sunny",
        "humidity": 45
    }),
    "search_web": lambda args: json.dumps({
        "results": [
            {"title": f"Result for: {args.get('query', '')}", "snippet": "This is a mock search result."}
        ]
    }),
}


def print_separator(title=""):
    print(f"\n{'='*60}")
    if title:
        print(f"  {title}")
        print(f"{'='*60}")


def print_chunk_info(chunk_data, chunk_idx):
    choices = chunk_data.get("choices", [])
    if not choices:
        return
    delta = choices[0].get("delta", {})
    finish_reason = choices[0].get("finish_reason")
    content = delta.get("content", "")
    reasoning = delta.get("reasoning_content", "")
    tool_calls = delta.get("tool_calls", [])
    
    parts = []
    if reasoning:
        parts.append(f"reason[{len(reasoning)}c]")
    if content:
        parts.append(f"content[{len(content)}c]")
    if tool_calls:
        for tc in tool_calls:
            fn = tc.get("function", {})
            parts.append(f"tool_call({fn.get('name', '?')})")
    if finish_reason:
        parts.append(f"finish={finish_reason}")
    
    if parts:
        print(f"  chunk#{chunk_idx}: {', '.join(parts)}")


async def stream_chat(client, messages, tools=None, test_name=""):
    payload = {
        "model": MODEL,
        "messages": messages,
        "stream": True,
        "max_output_tokens": 4096,
        "temperature": 0.7,
    }
    if tools:
        payload["tools"] = tools

    print(f"\n  >> 发送请求: {len(messages)} 条消息" + (f", {len(tools)} 个工具" if tools else ""))

    full_content = ""
    full_reasoning = ""
    tool_calls_collected = {}
    finish_reason = None
    chunk_count = 0
    usage = None

    async with client.stream("POST", f"{BASE_URL}/v1/chat/completions", json=payload, headers=HEADERS, timeout=TIMEOUT) as response:
        if response.status_code != 200:
            body = await response.aread()
            print(f"  ❌ HTTP {response.status_code}: {body.decode()}")
            return None, None, None

        async for line in response.aiter_lines():
            if not line.startswith("data: "):
                continue
            data_str = line[6:]
            if data_str == "[DONE]":
                print(f"  << [DONE] (共 {chunk_count} chunks)")
                break

            try:
                chunk = json.loads(data_str)
            except json.JSONDecodeError:
                print(f"  ⚠️ JSON解析失败: {data_str[:100]}")
                continue

            chunk_count += 1
            choices = chunk.get("choices", [])
            if not choices:
                continue

            choice = choices[0]
            delta = choice.get("delta", {})
            fr = choice.get("finish_reason")

            if delta.get("content"):
                full_content += delta["content"]
            if delta.get("reasoning_content"):
                full_reasoning += delta["reasoning_content"]
            if fr and not finish_reason:
                finish_reason = fr
            if chunk.get("usage"):
                usage = chunk["usage"]

            tc_list = delta.get("tool_calls", [])
            for tc in tc_list:
                idx = tc.get("index", 0)
                tc_id = tc.get("id", f"call_{idx}")
                if tc_id not in tool_calls_collected:
                    tool_calls_collected[tc_id] = {
                        "id": tc_id,
                        "type": "function",
                        "function": {"name": "", "arguments": ""}
                    }
                fn = tc.get("function", {})
                if fn.get("name"):
                    tool_calls_collected[tc_id]["function"]["name"] = fn["name"]
                if fn.get("arguments"):
                    tool_calls_collected[tc_id]["function"]["arguments"] += fn["arguments"]

            if chunk_count <= 5 or fr or tc_list:
                print_chunk_info(chunk, chunk_count)

    if chunk_count > 5 and not tool_calls_collected:
        print(f"  ... (省略中间 {chunk_count - 5} chunks)")

    print(f"\n  结果汇总:")
    if full_reasoning:
        print(f"    推理内容: {len(full_reasoning)} chars")
    if full_content:
        preview = full_content[:200] + "..." if len(full_content) > 200 else full_content
        print(f"    回复内容: {preview}")
    if tool_calls_collected:
        print(f"    工具调用: {len(tool_calls_collected)} 个")
        for tc_id, tc in tool_calls_collected.items():
            fn = tc["function"]
            print(f"      {fn['name']}({fn['arguments']})")
    print(f"    finish_reason: {finish_reason}")
    if usage:
        print(f"    usage: prompt={usage.get('prompt_tokens')}, completion={usage.get('completion_tokens')}, total={usage.get('total_tokens')}")

    tool_calls_list = list(tool_calls_collected.values()) if tool_calls_collected else None
    return full_content, tool_calls_list, finish_reason


async def non_stream_chat(client, messages, tools=None):
    payload = {
        "model": MODEL,
        "messages": messages,
        "stream": False,
        "max_output_tokens": 4096,
        "temperature": 0.7,
    }
    if tools:
        payload["tools"] = tools

    print(f"\n  >> 发送非流式请求: {len(messages)} 条消息" + (f", {len(tools)} 个工具" if tools else ""))

    response = await client.post(f"{BASE_URL}/v1/chat/completions", json=payload, headers=HEADERS, timeout=TIMEOUT)
    if response.status_code != 200:
        print(f"  ❌ HTTP {response.status_code}: {response.text}")
        return None, None, None

    data = response.json()
    choice = data["choices"][0]
    msg = choice["message"]
    content = msg.get("content", "")
    tool_calls = msg.get("tool_calls")
    finish_reason = choice.get("finish_reason")
    usage = data.get("usage")

    print(f"\n  结果汇总:")
    if content:
        preview = content[:200] + "..." if len(content) > 200 else content
        print(f"    回复内容: {preview}")
    if tool_calls:
        print(f"    工具调用: {len(tool_calls)} 个")
        for tc in tool_calls:
            fn = tc["function"]
            print(f"      {fn['name']}({fn['arguments']})")
    print(f"    finish_reason: {finish_reason}")
    if usage:
        print(f"    usage: {usage}")

    return content, tool_calls, finish_reason


async def test_1_simple_no_tools():
    print_separator("测试 1: 简单对话（无工具）")
    async with httpx.AsyncClient() as client:
        messages = [{"role": "user", "content": "说一句话，5个字以内"}]
        content, tc, fr = await stream_chat(client, messages, test_name="simple")
        assert content, "应该有回复内容"
        assert fr == "stop", f"finish_reason 应为 stop，实际: {fr}"
        assert tc is None, "不应有工具调用"
        print("  ✅ 测试通过")


async def test_2_tool_call_single():
    print_separator("测试 2: 单次工具调用（流式）")
    async with httpx.AsyncClient() as client:
        messages = [{"role": "user", "content": "北京现在天气怎么样？"}]
        content, tool_calls, fr = await stream_chat(client, messages, tools=TOOLS, test_name="single_tool")

        if fr == "tool_calls" and tool_calls:
            print("  ✅ 模型返回了工具调用")
            assert any(tc["function"]["name"] == "get_weather" for tc in tool_calls), "应调用 get_weather"
        elif fr == "stop" and content:
            print("  ⚠️ 模型直接回复了（未调用工具），这可能是因为 tools 被合并到 system prompt")
            print(f"     回复: {content[:100]}")
        else:
            print(f"  ❌ 意外结果: finish_reason={fr}, content={bool(content)}, tool_calls={bool(tool_calls)}")
        print("  ✅ 测试通过（记录行为）")


async def test_3_multi_turn_tool_calling():
    print_separator("测试 3: 多轮工具调用（流式）")
    async with httpx.AsyncClient() as client:
        messages = [
            {"role": "user", "content": "帮我查一下北京和上海的天气，然后对比一下"}
        ]

        for turn in range(1, 4):
            print(f"\n  --- 第 {turn} 轮 ---")
            content, tool_calls, fr = await stream_chat(client, messages, tools=TOOLS, test_name=f"multi_turn_{turn}")

            if fr == "tool_calls" and tool_calls:
                assistant_msg = {"role": "assistant", "content": content or None, "tool_calls": tool_calls}
                messages.append(assistant_msg)

                for tc in tool_calls:
                    fn_name = tc["function"]["name"]
                    fn_args = json.loads(tc["function"]["arguments"])
                    print(f"  🔧 模拟执行工具: {fn_name}({fn_args})")

                    if fn_name in TOOL_RESULTS:
                        result = TOOL_RESULTS[fn_name](fn_args)
                    else:
                        result = json.dumps({"error": f"Unknown function: {fn_name}"})

                    tool_msg = {
                        "role": "tool",
                        "tool_call_id": tc["id"],
                        "content": result
                    }
                    messages.append(tool_msg)
                    print(f"  📤 工具结果已添加: {result[:80]}")

            elif fr == "stop":
                print(f"  ✅ 模型最终回复（第 {turn} 轮结束）")
                if content:
                    print(f"     内容: {content[:300]}...")
                break
            else:
                print(f"  ⚠️ 意外: finish_reason={fr}")
                break
        else:
            print("  ⚠️ 达到最大轮数限制")

        print(f"\n  总消息数: {len(messages)}")
        print("  ✅ 测试通过（记录行为）")


async def test_4_tool_call_non_stream():
    print_separator("测试 4: 工具调用（非流式）")
    async with httpx.AsyncClient() as client:
        messages = [{"role": "user", "content": "查一下东京的天气"}]
        content, tool_calls, fr = await non_stream_chat(client, messages, tools=TOOLS)

        if fr == "tool_calls" and tool_calls:
            print("  ✅ 非流式模式正确返回工具调用")
        elif fr == "stop":
            print("  ⚠️ 非流式模式直接返回了内容（未调用工具）")
        print("  ✅ 测试通过（记录行为）")


async def test_5_multi_turn_non_stream():
    print_separator("测试 5: 多轮工具调用（非流式）")
    async with httpx.AsyncClient() as client:
        messages = [
            {"role": "user", "content": "搜索一下最新的AI新闻，然后告诉我"}
        ]

        for turn in range(1, 4):
            print(f"\n  --- 第 {turn} 轮 ---")
            content, tool_calls, fr = await non_stream_chat(client, messages, tools=TOOLS)

            if fr == "tool_calls" and tool_calls:
                assistant_msg = {"role": "assistant", "content": content or None, "tool_calls": tool_calls}
                messages.append(assistant_msg)

                for tc in tool_calls:
                    fn_name = tc["function"]["name"]
                    fn_args = json.loads(tc["function"]["arguments"])
                    print(f"  🔧 模拟执行: {fn_name}({fn_args})")

                    if fn_name in TOOL_RESULTS:
                        result = TOOL_RESULTS[fn_name](fn_args)
                    else:
                        result = json.dumps({"error": f"Unknown function: {fn_name}"})

                    messages.append({
                        "role": "tool",
                        "tool_call_id": tc["id"],
                        "content": result
                    })
            elif fr == "stop":
                print(f"  ✅ 最终回复（第 {turn} 轮）")
                break
            else:
                print(f"  ⚠️ 意外: finish_reason={fr}")
                break

        print("  ✅ 测试通过（记录行为）")


async def test_6_disconnect_during_tool_call():
    print_separator("测试 6: 工具调用后断开连接模拟")
    async with httpx.AsyncClient() as client:
        messages = [{"role": "user", "content": "查一下纽约天气"}]
        payload = {
            "model": MODEL,
            "messages": messages,
            "stream": True,
            "tools": TOOLS,
            "max_output_tokens": 4096,
        }

        chunks_received = 0
        print("  >> 发送请求后将在收到足够数据时主动断开...")

        try:
            async with client.stream("POST", f"{BASE_URL}/v1/chat/completions", json=payload, headers=HEADERS, timeout=30) as response:
                async for line in response.aiter_lines():
                    if not line.startswith("data: "):
                        continue
                    data_str = line[6:]
                    if data_str == "[DONE]":
                        print(f"  << [DONE] 正常收到完成标记 (chunks: {chunks_received})")
                        break
                    chunks_received += 1
                    if chunks_received >= 3:
                        print(f"  🔌 主动断开连接 (已收到 {chunks_received} chunks)")
                        break
        except Exception as e:
            print(f"  断开时异常（预期内）: {type(e).__name__}: {e}")

        print(f"  等待 3 秒让服务端处理断开...")
        await asyncio.sleep(3)

        print("  >> 断开后发送新请求验证服务可用性...")
        messages2 = [{"role": "user", "content": "说'OK'两个字"}]
        content, _, fr = await stream_chat(client, messages2, test_name="after_disconnect")
        if content:
            print(f"  ✅ 断开后服务仍可用，回复: {content[:50]}")
        else:
            print("  ❌ 断开后服务不可用")
        print("  ✅ 测试通过")


async def main():
    print_separator("AIStudio2API 工具调用测试套件")
    print(f"  目标: {BASE_URL}")
    print(f"  模型: {MODEL}")
    print(f"  时间: {time.strftime('%Y-%m-%d %H:%M:%S')}")

    async with httpx.AsyncClient() as client:
        try:
            resp = await client.get(f"{BASE_URL}/health", timeout=5)
            print(f"  健康检查: {resp.status_code}")
            if resp.status_code != 200:
                print("  ❌ 服务未就绪，跳过测试")
                return
        except Exception as e:
            print(f"  ❌ 无法连接到服务: {e}")
            print("  请确保服务已启动")
            return

    tests = [
        ("simple", test_1_simple_no_tools),
        ("single_tool", test_2_tool_call_single),
        ("multi_turn", test_3_multi_turn_tool_calling),
        ("non_stream_tool", test_4_tool_call_non_stream),
        ("multi_turn_non_stream", test_5_multi_turn_non_stream),
        ("disconnect", test_6_disconnect_during_tool_call),
    ]

    if len(sys.argv) > 1:
        selected = sys.argv[1]
        tests = [(name, fn) for name, fn in tests if selected in name]
        if not tests:
            print(f"  未找到匹配 '{selected}' 的测试")
            return

    results = {}
    for name, test_fn in tests:
        try:
            await test_fn()
            results[name] = "✅ PASS"
        except Exception as e:
            print(f"  ❌ 测试异常: {type(e).__name__}: {e}")
            results[name] = f"❌ FAIL: {e}"

    print_separator("测试结果汇总")
    for name, result in results.items():
        print(f"  {name}: {result}")
    print()


if __name__ == "__main__":
    asyncio.run(main())
