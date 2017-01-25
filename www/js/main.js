

function show_bar_chart(data) {
	var options = {
	  width: 600,
	  height: 300
	};

	//var data = {
	//	labels: [1, 2, 3, 4],
	//	series: [[5, 2, 8, 3], [1, 3, 7, 2]]
	//};

	new Chartist.Bar('#monthly_chart', data, options);
}

function show_report(userid, team) {
	var good = function(resp) {
		resp = JSON.parse(resp.response);
		var data = {
			labels: [],
			series: [[],[]],
		}
		for (var i = 0; i < resp.Months.length; i++) {
			var m = resp.Months[i];
			var totalDevTime = m.FeatureSeconds + m.BugSeconds;
			var bugDevPercent = 100 * m.BugSeconds / totalDevTime;
			data.labels.push(m.Month + " (" + bugDevPercent.toFixed(0) + "%)");
			data.series[0].push(m.FeatureSeconds / 3600);
			data.series[1].push(m.BugSeconds / 3600);
		}
		show_bar_chart(data);
	};
	url = "";
	if (userid)
		url = "/monthly?userid=" + userid;
	else
		url = "/monthly?team=" + team;
	$http({method: "GET", url: url, good: good});
}

$id('select_user').onchange = function(t) {
	show_report(t.target.value, undefined);
};

$id('select_team').onchange = function(t) {
	show_report(undefined, t.target.value);
};


