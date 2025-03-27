using Microsoft.AspNetCore.Mvc;
using System.Text.RegularExpressions;
using TokenAppCenter.Service;

namespace TokenAppCenter.Controllers;

[ApiController]
[Route("[controller]")]
public partial class AppCenterController(ConfigHubClient configHubClient) : ControllerBase
{
    [HttpGet("List")]
    public async Task<IActionResult> GetAppList(
        [FromHeader(Name = "iwut-platform")] string platform, 
        [FromHeader(Name = "iwut-version")] string iwutVersion)
    {
        var appConfig = await configHubClient.GetAppCenterConfig(platform).ConfigureAwait(false) ?? new();
        var appList = appConfig.App.Where(x => CheckVersion(iwutVersion, x.Version)).Select(x => new
        {
            x.Id, x.Name, x.Url, x.Icon, x.Show
        });

        var result = new
        {
            Code = 0,
            Message = "获取轻应用列表成功",
            Data = appList
        };

        return new JsonResult(result);
    }

    private static bool CheckVersion(string iwutVersion, string appVersionDesc)
    {
        var match = AppVersionRegex().Match(appVersionDesc);

        if (!match.Success) return false;

        var iwutVersionObj = Version.Parse(iwutVersion);
        var appVersionObj = Version.Parse(match.Groups[2].Value);

        return match.Groups[1].Value switch
        {
            ">" => iwutVersionObj > appVersionObj,
            ">=" => iwutVersionObj >= appVersionObj,
            "=" => iwutVersionObj == appVersionObj,
            "<=" => iwutVersionObj <= appVersionObj,
            "<" => iwutVersionObj < appVersionObj,
            _ => false
        };
    }

    [GeneratedRegex(@"([>=<]+)(\d+\.\d+\.\d+)")]
    private static partial Regex AppVersionRegex();
}
