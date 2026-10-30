package net

// enrollmentHTML is the HTML form for player enrollment.
const enrollmentHTML = `
<!DOCTYPE html>
<html lang="en">
<head>
	<meta charset="UTF-8">
	<meta name="viewport" content="width=device-width, initial-scale=1.0">
	<title>Galactic Bloodshed - Player Enrollment</title>
	<style>
		body {
			font-family: 'Courier New', monospace;
			background: #000 url('data:image/svg+xml,<svg xmlns="http://www.w3.org/2000/svg" width="100" height="100"><circle cx="10" cy="10" r="1" fill="%23444"/></svg>');
			color: #0f0;
			max-width: 900px;
			margin: 0 auto;
			padding: 20px;
		}
		.container {
			border: 2px solid #0f0;
			padding: 20px;
			background: rgba(0, 50, 0, 0.3);
		}
		h1 {
			text-align: center;
			color: #0f0;
			text-shadow: 0 0 10px #0f0;
		}
		.form-group {
			margin-bottom: 15px;
		}
		label {
			display: block;
			margin-bottom: 5px;
			font-weight: bold;
		}
		input[type="text"],
		input[type="password"],
		input[type="number"],
		input[type="range"],
		textarea,
		select {
			width: 100%;
			padding: 8px;
			background: #001a00;
			border: 1px solid #0f0;
			color: #0f0;
			font-family: 'Courier New', monospace;
			box-sizing: border-box;
		}
		textarea {
			resize: vertical;
			min-height: 80px;
		}
		input:focus, textarea:focus, select:focus {
			outline: none;
			box-shadow: 0 0 10px #0f0;
		}
		.slider-container {
			display: grid;
			grid-template-columns: 1fr 50px;
			gap: 10px;
			align-items: center;
		}
		.slider-value {
			text-align: right;
			background: #001a00;
			border: 1px solid #0f0;
			padding: 5px;
			border-radius: 3px;
		}
		.sector-compat {
			display: grid;
			grid-template-columns: repeat(4, 1fr);
			gap: 10px;
		}
		.sector-item {
			border: 1px solid #0f0;
			padding: 10px;
			background: rgba(0, 50, 0, 0.3);
		}
		.sector-item label {
			font-size: 0.85em;
			margin-bottom: 5px;
		}
		.sector-item input {
			width: 100%;
		}
		button {
			background: #0f0;
			color: #000;
			border: none;
			padding: 12px 30px;
			font-size: 1em;
			font-weight: bold;
			cursor: pointer;
			width: 100%;
			margin-top: 20px;
			transition: all 0.3s;
		}
		button:hover {
			background: #0a0;
			box-shadow: 0 0 20px #0f0;
		}
		button:disabled {
			background: #666;
			cursor: not-allowed;
			box-shadow: none;
		}
		.message {
			margin-top: 20px;
			padding: 15px;
			border: 1px solid #0f0;
			border-radius: 3px;
		}
		.success {
			background: rgba(0, 100, 0, 0.5);
			border-color: #0f0;
			color: #0f0;
		}
		.error {
			background: rgba(100, 0, 0, 0.5);
			border-color: #f00;
			color: #f00;
		}
		.error-list {
			list-style: none;
			padding-left: 0;
		}
		.error-list li:before {
			content: "⚠ ";
			color: #f00;
		}
		.required {
			color: #f00;
		}
		.section-title {
			font-weight: bold;
			margin-top: 20px;
			margin-bottom: 10px;
			color: #0a0;
		}
		.help-text {
			font-size: 0.85em;
			color: #0a0;
			font-style: italic;
		}
	</style>
</head>
<body>
	<div class="container">
		<h1>⚡ GALACTIC BLOODSHED ⚡</h1>
		<h2 style="text-align: center;">Player Enrollment Form</h2>
		
		<form id="enrollmentForm">
			<!-- Basic Info -->
			<div class="section-title">BASIC INFORMATION</div>
			
			<div class="form-group">
				<label for="raceName">Race Name <span class="required">*</span></label>
				<input type="text" id="raceName" name="race_name" placeholder="e.g., Terrans, Altairans" required>
				<div class="help-text">The name of your race/empire</div>
			</div>
			
			<div class="form-group">
				<label for="password">Password <span class="required">*</span></label>
				<input type="password" id="password" name="password" placeholder="Your password" required minlength="3">
				<div class="help-text">At least 3 characters</div>
			</div>
			
			<div class="form-group">
				<label for="info">Race Description</label>
				<textarea id="info" name="info" placeholder="Optional flavor text describing your race..."></textarea>
			</div>

			<!-- Racial Traits -->
			<div class="section-title">RACIAL TRAITS</div>
			
			<div class="form-group">
				<label for="iq">Intelligence (IQ) <span class="required">*</span></label>
				<div class="slider-container">
					<input type="range" id="iq" name="iq" min="1" max="100" value="50" step="1">
					<div class="slider-value"><span id="iqValue">50</span></div>
				</div>
				<div class="help-text">Range: 1–100 (higher = faster tech)</div>
			</div>
			
			<div class="form-group">
				<label for="fertilize">Fertility Rate <span class="required">*</span></label>
				<div class="slider-container">
					<input type="range" id="fertilize" name="fertilize" min="1" max="100" value="50" step="1">
					<div class="slider-value"><span id="fertilizeValue">50</span></div>
				</div>
				<div class="help-text">Range: 1–100 (higher = faster population growth)</div>
			</div>
			
			<div class="form-group">
				<label for="birthRate">Birth Rate <span class="required">*</span></label>
				<div class="slider-container">
					<input type="range" id="birthRate" name="birth_rate" min="0.25" max="1.0" value="0.5" step="0.05">
					<div class="slider-value"><span id="birthRateValue">0.50</span></div>
				</div>
				<div class="help-text">Range: 0.25–1.0 (population per sector)</div>
			</div>
			
			<div class="form-group">
				<label for="mass">Mass <span class="required">*</span></label>
				<div class="slider-container">
					<input type="range" id="mass" name="mass" min="0.1" max="10.0" value="1.0" step="0.1">
					<div class="slider-value"><span id="massValue">1.0</span></div>
				</div>
				<div class="help-text">Range: 0.1–10.0 (affects fuel consumption)</div>
			</div>
			
			<div class="form-group">
				<label for="metabolism">Metabolism <span class="required">*</span></label>
				<div class="slider-container">
					<input type="range" id="metabolism" name="metabolism" min="0.1" max="10.0" value="1.0" step="0.1">
					<div class="slider-value"><span id="metabolismValue">1.0</span></div>
				</div>
				<div class="help-text">Range: 0.1–10.0 (food consumption rate)</div>
			</div>
			
			<div class="form-group">
				<label for="numberSexes">Number of Sexes <span class="required">*</span></label>
				<input type="number" id="numberSexes" name="number_sexes" min="1" max="15" value="2" required>
				<div class="help-text">Range: 1–15</div>
			</div>
			
			<div class="form-group">
				<label for="adventurism">Adventurism <span class="required">*</span></label>
				<div class="slider-container">
					<input type="range" id="adventurism" name="adventurism" min="0" max="100" value="50" step="1">
					<div class="slider-value"><span id="adventurismValue">50</span></div>
				</div>
				<div class="help-text">Range: 0–100 (tendency toward warfare/expansion)</div>
			</div>

			<!-- Home Planet -->
			<div class="section-title">HOME PLANET TYPE</div>
			
			<div class="form-group">
				<label for="homePlanetType">Preferred Home Planet <span class="required">*</span></label>
				<select id="homePlanetType" name="home_planet_type" required>
					<option value="0">Earth (balanced)</option>
					<option value="1">Forest (organic)</option>
					<option value="2">Desert (harsh)</option>
					<option value="3">Water (aquatic)</option>
					<option value="4">Airless (void-adapted)</option>
					<option value="5">Iceball (cold)</option>
					<option value="6">Jovian (gas giant adapted)</option>
				</select>
			</div>

			<!-- Sector Compatibility -->
			<div class="section-title">SECTOR COMPATIBILITY (0–100)</div>
			<div class="help-text" style="margin-bottom: 15px;">How well your race adapts to each terrain type. Higher = better productivity.</div>
			
			<div class="sector-compat">
				<div class="sector-item">
					<label for="sectorCompat0">Land</label>
					<input type="range" id="sectorCompat0" name="sector_compat_0" min="0" max="100" value="100" step="1">
					<div style="text-align: right; font-size: 0.8em;"><span id="sc0v">100</span></div>
				</div>
				<div class="sector-item">
					<label for="sectorCompat1">Mountain</label>
					<input type="range" id="sectorCompat1" name="sector_compat_1" min="0" max="100" value="75" step="1">
					<div style="text-align: right; font-size: 0.8em;"><span id="sc1v">75</span></div>
				</div>
				<div class="sector-item">
					<label for="sectorCompat2">Sea</label>
					<input type="range" id="sectorCompat2" name="sector_compat_2" min="0" max="100" value="75" step="1">
					<div style="text-align: right; font-size: 0.8em;"><span id="sc2v">75</span></div>
				</div>
				<div class="sector-item">
					<label for="sectorCompat3">Gas</label>
					<input type="range" id="sectorCompat3" name="sector_compat_3" min="0" max="100" value="0" step="1">
					<div style="text-align: right; font-size: 0.8em;"><span id="sc3v">0</span></div>
				</div>
				<div class="sector-item">
					<label for="sectorCompat4">Desert</label>
					<input type="range" id="sectorCompat4" name="sector_compat_4" min="0" max="100" value="50" step="1">
					<div style="text-align: right; font-size: 0.8em;"><span id="sc4v">50</span></div>
				</div>
				<div class="sector-item">
					<label for="sectorCompat5">Forest</label>
					<input type="range" id="sectorCompat5" name="sector_compat_5" min="0" max="100" value="75" step="1">
					<div style="text-align: right; font-size: 0.8em;"><span id="sc5v">75</span></div>
				</div>
				<div class="sector-item">
					<label for="sectorCompat6">Swamp</label>
					<input type="range" id="sectorCompat6" name="sector_compat_6" min="0" max="100" value="25" step="1">
					<div style="text-align: right; font-size: 0.8em;"><span id="sc6v">25</span></div>
				</div>
				<div class="sector-item">
					<label for="sectorCompat7">Waste</label>
					<input type="range" id="sectorCompat7" name="sector_compat_7" min="0" max="100" value="25" step="1">
					<div style="text-align: right; font-size: 0.8em;"><span id="sc7v">25</span></div>
				</div>
			</div>

			<button type="submit">ENROLL RACE</button>
			
			<div id="message"></div>
		</form>
	</div>

	<script>
		// Update slider value displays
		function setupSlider(id, displayId) {
			const slider = document.getElementById(id);
			if (!slider) return;
			
			const update = () => {
				const val = parseFloat(slider.value).toFixed(slider.step && slider.step < 1 ? 2 : 0);
				document.getElementById(displayId).textContent = val;
			};
			
			slider.addEventListener('input', update);
		}

		setupSlider('iq', 'iqValue');
		setupSlider('fertilize', 'fertilizeValue');
		setupSlider('birthRate', 'birthRateValue');
		setupSlider('mass', 'massValue');
		setupSlider('metabolism', 'metabolismValue');
		setupSlider('adventurism', 'adventurismValue');

		// Sector compat sliders
		for (let i = 0; i < 8; i++) {
			const slider = document.getElementById('sectorCompat' + i);
			if (slider) {
				slider.addEventListener('input', function() {
					document.getElementById('sc' + i + 'v').textContent = this.value;
				});
			}
		}

		// Form submission
		document.getElementById('enrollmentForm').addEventListener('submit', async (e) => {
			e.preventDefault();
			
			const form = e.target;
			const messageDiv = document.getElementById('message');
			
			const data = {
				race_name: document.getElementById('raceName').value,
				password: document.getElementById('password').value,
				info: document.getElementById('info').value,
				home_planet_type: parseInt(document.getElementById('homePlanetType').value),
				iq: parseInt(document.getElementById('iq').value),
				fertilize: parseInt(document.getElementById('fertilize').value),
				birth_rate: parseFloat(document.getElementById('birthRate').value),
				mass: parseFloat(document.getElementById('mass').value),
				metabolism: parseFloat(document.getElementById('metabolism').value),
				number_sexes: parseInt(document.getElementById('numberSexes').value),
				adventurism: parseFloat(document.getElementById('adventurism').value),
				sector_compat: [
					parseFloat(document.getElementById('sectorCompat0').value),
					parseFloat(document.getElementById('sectorCompat1').value),
					parseFloat(document.getElementById('sectorCompat2').value),
					parseFloat(document.getElementById('sectorCompat3').value),
					parseFloat(document.getElementById('sectorCompat4').value),
					parseFloat(document.getElementById('sectorCompat5').value),
					parseFloat(document.getElementById('sectorCompat6').value),
					parseFloat(document.getElementById('sectorCompat7').value),
				]
			};

			try {
				const response = await fetch('/api/enroll', {
					method: 'POST',
					headers: {'Content-Type': 'application/json'},
					body: JSON.stringify(data)
				});
				
				const result = await response.json();
				
				if (result.success) {
					messageDiv.className = 'message success';
					messageDiv.innerHTML = '<strong>✓ Enrollment Successful!</strong><br>' + result.message;
					form.reset();
				} else {
					messageDiv.className = 'message error';
					let msg = '<strong>✗ Enrollment Failed</strong><br>' + result.message;
					if (result.errors && result.errors.length > 0) {
						msg += '<ul class="error-list">' + result.errors.map(e => '<li>' + e + '</li>').join('') + '</ul>';
					}
					messageDiv.innerHTML = msg;
				}
			} catch (err) {
				messageDiv.className = 'message error';
				messageDiv.innerHTML = '<strong>✗ Error</strong><br>' + err.message;
			}
		});
	</script>
</body>
</html>
`
