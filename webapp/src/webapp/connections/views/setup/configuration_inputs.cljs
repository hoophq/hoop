(ns webapp.connections.views.setup.configuration-inputs
  (:require
   ["@radix-ui/themes" :refer [Box Button Grid Heading Text]]
   ["lucide-react" :refer [Plus]]
   [clojure.string :as str]
   [re-frame.core :as rf]
   [webapp.components.forms :as forms]
   [webapp.connections.views.setup.connection-method :as connection-method]))

(defn valid-first-char? [value]
  (boolean (re-matches #"[A-Za-z]" value)))

(defn valid-posix? [value]
  (boolean (re-matches #"[A-Za-z][A-Za-z0-9_]*" value)))

(defn valid-header-key? [value]
  (boolean (re-matches #"\S+" value)))

(defn parse-env-key [e]
  (let [new-value (-> e .-target .-value)
        current-value (get-in @(rf/subscribe [:connection-setup/form-data])
                              [:credentials :current-key])]
    (cond
      (empty? new-value)
      (rf/dispatch [:connection-setup/update-env-current-key ""])

      ;; Handle paste operation
      (> (count new-value) 1)
      (when (valid-posix? new-value)
        (rf/dispatch [:connection-setup/update-env-current-key new-value]))

      (empty? current-value)
      (when (valid-first-char? new-value)
        (rf/dispatch [:connection-setup/update-env-current-key new-value]))

      (valid-posix? new-value)
      (rf/dispatch [:connection-setup/update-env-current-key new-value]))))

(defn parse-config-file-name [e]
  (let [new-value (-> e .-target .-value)
        upper-value (str/upper-case new-value)
        current-value (get-in @(rf/subscribe [:connection-setup/form-data])
                              [:credentials :current-file-name])]
    (cond
      (empty? new-value)
      (rf/dispatch [:connection-setup/update-config-file-name ""])

      ;; Handle paste operation
      (> (count new-value) 1)
      (when (valid-posix? new-value)
        (rf/dispatch [:connection-setup/update-config-file-name upper-value]))

      (empty? current-value)
      (when (valid-first-char? new-value)
        (rf/dispatch [:connection-setup/update-config-file-name upper-value]))

      (valid-posix? new-value)
      (rf/dispatch [:connection-setup/update-config-file-name upper-value]))))

(defn parse-existing-env-key [e index]
  (let [new-value (-> e .-target .-value)
        env-vars (get-in @(rf/subscribe [:connection-setup/form-data])
                         [:credentials :environment-variables])
        current-value (get-in env-vars [index :key])]
    (cond
      (empty? new-value)
      (rf/dispatch [:connection-setup/update-env-var index :key ""])

      ;; Handle paste operation
      (> (count new-value) 1)
      (when (valid-posix? new-value)
        (rf/dispatch [:connection-setup/update-env-var index :key new-value]))

      (empty? current-value)
      (when (valid-first-char? new-value)
        (rf/dispatch [:connection-setup/update-env-var index :key new-value]))

      (valid-posix? new-value)
      (rf/dispatch [:connection-setup/update-env-var index :key new-value]))))

(defn parse-header-key [e]
  (let [new-value (-> e .-target .-value)]
    (cond
      (empty? new-value)
      (rf/dispatch [:connection-setup/update-env-current-key ""])

      (valid-header-key? new-value)
      (rf/dispatch [:connection-setup/update-env-current-key new-value]))))

(defn parse-existing-header-key [e index]
  (let [new-value (-> e .-target .-value)]
    (cond
      (empty? new-value)
      (rf/dispatch [:connection-setup/update-env-var index :key ""])

      (valid-header-key? new-value)
      (rf/dispatch [:connection-setup/update-env-var index :key new-value]))))

(defn parse-existing-config-file-name [e index]
  (let [new-value (-> e .-target .-value)
        upper-value (str/upper-case new-value)
        config-files (get-in @(rf/subscribe [:connection-setup/form-data])
                             [:credentials :configuration-files])
        current-value (get-in config-files [index :key])]
    (cond
      (empty? new-value)
      (rf/dispatch [:connection-setup/update-config-file index :key ""])

      ;; Handle paste operation
      (> (count new-value) 1)
      (when (valid-posix? new-value)
        (rf/dispatch [:connection-setup/update-config-file index :key upper-value]))

      (empty? current-value)
      (when (valid-first-char? new-value)
        (rf/dispatch [:connection-setup/update-config-file index :key upper-value]))

      (valid-posix? new-value)
      (rf/dispatch [:connection-setup/update-config-file index :key upper-value]))))

(defn key-value-section
  [{:keys [title subtitle hide-title? key-placeholder add-label parse-key parse-existing-key]}]
  (let [current-key @(rf/subscribe [:connection-setup/env-current-key])
        current-value @(rf/subscribe [:connection-setup/env-current-value])
        env-vars @(rf/subscribe [:connection-setup/environment-variables])
        connection-method @(rf/subscribe [:connection-setup/connection-method])
        show-selector? (= connection-method "secrets-manager")]
    [:> Box {:class "space-y-4"}
     (when-not hide-title?
       [:<>
        [:> Heading {:size "3"} title]
        [:> Text {:size "2" :color "gray"} subtitle]])

     (when (seq env-vars)
       [:> Grid {:columns "2" :gap "2"}
        (for [[idx {:keys [key value]}] (map-indexed vector env-vars)]
          ^{:key (str "env-var-" idx)}
          [:<>
           [forms/input
            {:label "Key"
             :value key
             :placeholder key-placeholder
             :on-change #(parse-existing-key % idx)}]
           [forms/input
            {:label "Value"
             :value value
             :type "password"
             :placeholder "* * * *"
             :on-change #(rf/dispatch [:connection-setup/update-env-var
                                       idx
                                       :value
                                       (-> % .-target .-value)])
             :start-adornment (when show-selector?
                                [connection-method/source-selector
                                 (str "env-var-" idx)])}]])])

     [:> Grid {:columns "2" :gap "2"}
      [forms/input
       {:label "Key"
        :placeholder key-placeholder
        :value current-key
        :on-change parse-key}]
      [forms/input
       {:label "Value"
        :placeholder "* * * *"
        :type "password"
        :value current-value
        :on-change #(rf/dispatch [:connection-setup/update-env-current-value
                                  (-> % .-target .-value)])
        :start-adornment (when show-selector?
                           [connection-method/source-selector "env-current-value"])}]]

     [:> Button
      {:size "2"
       :variant "soft"
       :type "button"
       :on-click #(rf/dispatch [:connection-setup/add-env-row])}
      [:> Plus {:size 16}]
      add-label]]))

(defn environment-variables-section [{:keys [title subtitle hide-default-title]}]
  [key-value-section
   {:title (if title title "Environment variables")
    :subtitle (if subtitle subtitle "Add variable values to use in your resource role.")
    :hide-title? hide-default-title
    :key-placeholder "API_KEY"
    :add-label "Add"
    :parse-key parse-env-key
    :parse-existing-key parse-existing-env-key}])

(defn http-headers-section [{:keys [title subtitle hide-default-title]}]
  [key-value-section
   {:title (if title title "HTTP headers")
    :subtitle (if subtitle subtitle "Add HTTP headers that will be used in your requests.")
    :hide-title? hide-default-title
    :key-placeholder "X-Request-Id"
    :add-label "Add header"
    :parse-key parse-header-key
    :parse-existing-key parse-existing-header-key}])

(defn configuration-files-section []
  (let [config-files (rf/subscribe [:connection-setup/configuration-files])
        current-name (rf/subscribe [:connection-setup/config-current-name])
        current-content (rf/subscribe [:connection-setup/config-current-content])]
    [:> Box {:class "space-y-4"}
     [:> Heading {:size "3"} "Configuration files"]
     [:> Text {:size "2" :color "gray"}
      "Add values from your configuration file and use them as an environment variable in your resource role."]

     (when (seq @config-files)
       [:> Grid {:columns "1" :gap "4"}
        (for [[idx {:keys [key value]}] (map-indexed vector @config-files)]
          ^{:key (str "config-file-" idx)}
          [:<>
           [forms/input
            {:label "Name"
             :value key
             :placeholder "e.g. kubeconfig"
             :on-change #(parse-existing-config-file-name % idx)}]
           [forms/textarea
            {:label "Content"
             :id (str "config-file-" idx)
             :value value
             :on-change #(rf/dispatch [:connection-setup/update-config-file idx :value (-> % .-target .-value)])}]])])

     [:> Grid {:columns "1" :gap "4"}
      [forms/input
       {:label "Name"
        :placeholder "e.g. kubeconfig"
        :value @current-name
        :on-change #(parse-config-file-name %)}]
      [forms/textarea
       {:label "Content"
        :id "config-file-initial"
        :placeholder "Paste your file content here"
        :value @current-content
        :on-change #(rf/dispatch [:connection-setup/update-config-file-content
                                  (-> % .-target .-value)])}]]

     [:> Button
      {:size "2"
       :variant "soft"
       :type "button"
       :on-click #(when (and (not (empty? @current-name))
                             (not (empty? @current-content)))
                    (rf/dispatch [:connection-setup/add-configuration-file]))}
      [:> Plus {:size 16
                :class "mr-2"}]
      "Add file"]]))
