"""Model Context Protocol (MCP) usage fixture — positive and negative cases."""
from mcp.server.fastmcp import FastMCP  # airom: mcp/import

# airom: mcp/construct
server = FastMCP("my-tools")

# airom-ok: mcp/import
note = "mcp server setup guide"

# airom-ok: mcp/construct
label = "FastMCP configuration notes"
