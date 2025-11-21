(ns webapp.features.runbooks.runner.views.list
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Text Callout Link]]
   ["lucide-react" :refer [ChevronDown ChevronUp File Folder FolderOpen Info FolderGit2]]
   [clojure.string :as cs]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.tooltip :as tooltip]
   [webapp.config :as config]))

(defn sort-tree [data]
  (let [folders (->> data
                     (filter (fn [[k v]] (map? v)))
                     (sort-by first))
        files-or-list (->> data
                           (filter (fn [[k v]] (not (map? v))))
                           (sort-by first))]
    (into {} (concat folders files-or-list))))

(defn split-path [path]
  (cs/split path #"/"))

(defn extract-repo-name
  "Extract just the repository name from a full repository path.
   Example: 'github.com/hoophq/runbooks' -> 'runbooks'"
  [repo-path]
  (if (string? repo-path)
    (let [parts (cs/split repo-path #"/")]
      (last parts))
    repo-path))

(defn is-file? [item]
  (or (string? item)
      (and (vector? item)
           (every? string? item))))

(defn insert-into-tree [tree path original-name]
  (if (empty? (rest path))
    (update tree (first path) (fnil conj []) original-name)
    (let [dir (first path)
          rest-path (rest path)
          sub-tree (get tree dir {})]
      (assoc tree dir (insert-into-tree sub-tree rest-path original-name)))))

(defn transform-payload [payload]
  (let [grouped-by-type (group-by
                         (fn [{:keys [name]}]
                           (if (cs/includes? name "/")
                             :with-folder
                             :without-folder))
                         payload)

        folder-tree (reduce
                     (fn [tree {:keys [name]}]
                       (let [path-parts (split-path name)]
                         (insert-into-tree tree path-parts name)))
                     {}
                     (:with-folder grouped-by-type))

        root-files (map (fn [{:keys [name]}]
                          [name name])
                        (:without-folder grouped-by-type))]

    (into folder-tree root-files)))

(defn file [filename filter-template-selected level selected? repository]
  [:> Flex {:class (str "items-center gap-2 pb-3 hover:underline cursor-pointer text-xs text-gray-12 whitespace-pre overflow-x-hidden "
                        (when (pos? level) "pl-4"))
            :on-click (fn []
                        (let [template (filter-template-selected filename repository)]
                          (rf/dispatch [:runbooks/set-active-runbook template repository])))}
   [:> Flex {:class (str "w-fit gap-2 items-center py-1.5 px-2" (when selected?  " bg-[--indigo-a3] rounded-2"))}
    [:> File {:size 16
              :class "text-[--gray-11]"}]
    [:> Text {:size "2" :weight "medium" :class "flex items-center block truncate"}
     [tooltip/truncate-tooltip {:text (last (split-path filename))}]]]])

(defn directory []
  (let [dropdown-status (r/atom {})
        search-term (rf/subscribe [:search/term])
        selected-template (rf/subscribe [:runbooks-plugin->selected-runbooks])]

    (fn [name items level filter-template-selected parent-path repository]
      (let [current-path (if parent-path (str parent-path "/" name) name)
            selected-name (get-in @selected-template [:data :name])
            selected-repo (get-in @selected-template [:data :repository])
            is-selected? (and (= selected-name current-path)
                             (= selected-repo repository))
            ;; Make dropdown key unique per repository to avoid conflicts
            dropdown-key (str (hash repository) "-" name)]

        ;; Only auto-open if state is nil (not yet set), not if user manually closed it
        (when (and (seq @search-term)
                   (nil? (get @dropdown-status dropdown-key)))
          (swap! dropdown-status assoc dropdown-key :open))

        ;; Only auto-open if the selected item is in this repository
        ;; Only auto-open if state is nil (not yet set), not if user manually closed it
        (when (and selected-name
                   selected-repo
                   (= selected-repo repository)
                   (nil? (get @dropdown-status dropdown-key))
                   (or (= selected-name current-path)
                       (cs/starts-with? selected-name (str current-path "/"))))
          (swap! dropdown-status assoc dropdown-key :open))

        (if (is-file? items)
          [file current-path filter-template-selected level is-selected? repository]
          [:> Box {:class (str "text-xs text-gray-12 " (when (pos? level) "pl-4"))}
           [:> Flex {:class "flex pb-4 items-center gap-small cursor-pointer"
                     :on-click #(swap! dropdown-status
                                       assoc dropdown-key
                                       (if (= (get @dropdown-status dropdown-key) :open) :closed :open))}
            (if (= (get @dropdown-status dropdown-key) :open)
              [:> FolderOpen {:size 16
                              :class "text-[--gray-11]"}]
              [:> Folder {:size 16
                          :class "text-[--gray-11]"}])
            [:> Text {:size "2" :weight "medium" :class "hover:underline"} name]
            [:> Box
             (if (= (get @dropdown-status dropdown-key) :open)
               [:> ChevronUp {:size 16
                              :class "text-[--gray-11]"}]
               [:> ChevronDown {:size 16
                                :class "text-[--gray-11]"}])]]

           [:> Box {:class (when (not= (get @dropdown-status dropdown-key) :open)
                             "h-0 overflow-hidden")}
            (if (map? items)
              (let [subfolders-and-files (group-by (fn [[_ subcontents]]
                                                     (if (or (map? subcontents)
                                                             (and (vector? subcontents)
                                                                  (some map? subcontents)))
                                                       :folders
                                                       :files))
                                                   items)

                    sorted-subfolders (->> (get subfolders-and-files :folders {})
                                           (sort-by first))
                    sorted-files (->> (get subfolders-and-files :files {})
                                      (sort-by first))
                    child-level (inc level)]
                [:<>
                 (for [[subdirname subcontents] sorted-subfolders]
                   ^{:key subdirname}
                   [directory subdirname subcontents child-level filter-template-selected current-path repository])

                 (for [[filename subcontents] sorted-files]
                   ^{:key filename}
                   [directory filename subcontents child-level filter-template-selected current-path repository])])

              (for [item items]
                ^{:key item}
                (let [selected-name (get-in @selected-template [:data :name])
                      selected-repo (get-in @selected-template [:data :repository])
                      is-selected? (and (= selected-name item)
                                       (= selected-repo repository))]
                  [file item filter-template-selected (inc level) is-selected? repository])))]])))))

(defn directory-tree [tree filter-template-selected repository]
  (let [folders-and-files (group-by (fn [[_ contents]]
                                      (if (or (map? contents)
                                              (and (vector? contents)
                                                   (some map? contents)))
                                        :folders
                                        :files))
                                    tree)
        sorted-folders (->> (get folders-and-files :folders [])
                            (sort-by first))
        sorted-files (->> (get folders-and-files :files [])
                          (sort-by first))]

    [:> Box
     (for [[dirname contents] sorted-folders]
       ^{:key dirname}
       [directory dirname contents 0 filter-template-selected nil repository])

     (for [[filename contents] sorted-files]
       ^{:key filename}
       [directory filename contents 0 filter-template-selected nil repository])]))

(defn- loading-list-view []
  [:> Flex {:class "h-full text-center flex-col justify-center items-center"}
   [:> Text {:size "1" :class "text-gray-8"}
    "Loading runbooks"]
   [:> Box {:class "w-3 flex-shrink-0 animate-spin opacity-60"}
    [:img {:src (str config/webapp-url "/icons/icon-loader-circle-white.svg")}]]])

(defn- empty-templates-view []
  [:> Flex {:class "h-full text-center flex-col justify-center items-center"}
   [:> Text {:size "1" :class "text-gray-8"}
    "No Runbooks available"]
   [:> Text {:size "1" :class "text-gray-8"}
    "Contact your Admin for more information"]])

(defn- runbooks-extension-callout []
  [:> Callout.Root {:size "1"
                    :color "blue"
                    :highContrast true
                    :class "bg-info-3"}
   [:> Callout.Icon
    [:> Info {:size 16}]]
   [:> Callout.Text
    [:> Box {:class "flex flex-col gap-2 text-info-12"}
     [:> Text {:size "2"}
      "If you have already set your Runbooks and still don't see them, make sure your files include "
      [:> Text {:weight "medium"} ".runbooks"]
      " on the extension."]
     [:> Link {:href (get-in config/docs-url [:features :runbooks])
               :target "_blank"
               :class "text-info-12 font-medium"}
      "Go to Runbooks configuration docs ↗"]]]])

(defn- no-integration-templates-view []
  [:> Flex {:class "h-full flex-col"}
   [:> Box {:class "grow flex flex-col items-center justify-center text-center gap-4"}
    [:> Text {:size "1" :class "text-gray-8"}
     "No Runbooks configured on your Organization yet"]
    [:> Button {:color "indigo"
                :size "2"
                :variant "soft"
                :radius "medium"
                :on-click #(rf/dispatch [:navigate :runbooks-setup {:tab "configurations"}])}
     "Go to Runbooks Configuration"]]
   [:> Box {:class "self-end mt-8 mb-2 mx-4"}
    [runbooks-extension-callout]]])

(defn repository-folder []
  (let [dropdown-status (r/atom {})
        search-term (rf/subscribe [:search/term])
        selected-template (rf/subscribe [:runbooks-plugin->selected-runbooks])]
    (fn [repository filter-template-selected level]
      (let [repo-name (:repository repository)
            items (:items repository)
            repo-id (str "repo-" (hash repo-name))
            is-open? (= (get @dropdown-status repo-id) :open)
            has-items? (seq items)

            ;; Check if any item matches search or is selected
            selected-name (get-in @selected-template [:data :name])
            selected-repo (get-in @selected-template [:data :repository])
            has-selected? (and selected-name
                               selected-repo
                               (= selected-repo repo-name)
                               (some #(= (:name %) selected-name) items))
            has-search-match? (and (seq @search-term)
                                   (some #(cs/includes?
                                           (cs/lower-case (:name %))
                                           (cs/lower-case @search-term))
                                         items))
            _ (when (and (nil? (get @dropdown-status repo-id))
                         (or has-search-match? has-selected?))
                (swap! dropdown-status assoc repo-id :open))

            ;; Transform items for tree view
            display-items (if (seq @search-term)
                            (filter (fn [item]
                                     (cs/includes?
                                      (cs/lower-case (:name item))
                                      (cs/lower-case @search-term)))
                                   items)
                            items)
            transformed-payload (when (seq display-items)
                                (sort-tree (transform-payload display-items)))]

        (when has-items?
          (let [display-repo-name (extract-repo-name repo-name)]
            [:> Box {:class (str "text-xs text-gray-12 " (when (pos? level) "pl-4"))}
             [:> Flex {:class "flex pb-4 items-center gap-small cursor-pointer"
                       :on-click #(swap! dropdown-status
                                         assoc-in [repo-id]
                                         (if (= (get @dropdown-status repo-id) :open) :closed :open))}
              [:> FolderGit2 {:size 16
                              :class "text-[--gray-11]"}]
              [:> Text {:size "2" :weight "bold" :class "hover:underline"} display-repo-name]
              [:> Box
               (if is-open?
                 [:> ChevronUp {:size 16
                                :class "text-[--gray-11]"}]
                 [:> ChevronDown {:size 16
                                  :class "text-[--gray-11]"}])]]

             [:> Box {:class (when (not= (get @dropdown-status repo-id) :open)
                               "h-0 overflow-hidden")}
              [:> Box {:class "pl-4"}
               (if (seq display-items)
                 [directory-tree transformed-payload filter-template-selected repo-name]
                 [:> Text {:size "1" :class "text-gray-8 px-2"}
                  "No runbooks match the search criteria."])]]]))))))

(defn repositories-view [repositories filter-template-selected]
  [:> Box
   (for [repo repositories]
     ^{:key (:repository repo)}
     [repository-folder repo filter-template-selected 0])])

(defn main []
  (fn [templates filtered-templates]
    (let [templates-data (:data @templates)
          repositories (or (:repositories templates-data) [])
          all-items (or (:items templates-data) [])
          filter-template-selected (fn [template-name repository]
                                     (when repository
                                       (let [repo (first (filter #(= (:repository %) repository) repositories))]
                                         (when repo
                                           (first (filter #(= (:name %) template-name) (:items repo)))))))
          search-term (rf/subscribe [:search/term])
          filtered-data (or @filtered-templates [])
          has-search? (seq @search-term)

          ;; For backward compatibility: if no repositories, use old flat list
          use-repositories? (seq repositories)

          ;; For old format or search results
          display-templates (if has-search?
                              filtered-data
                              (if use-repositories?
                                []
                                all-items))
          transformed-payload (when (seq display-templates)
                               (sort-tree (transform-payload display-templates)))]

      (cond
        (= :loading (:status @templates)) [loading-list-view]
        (= :error (:status @templates)) [no-integration-templates-view]
        (and (empty? all-items) (empty? repositories) (= :success (:status @templates))) [empty-templates-view]
        (and use-repositories? (empty? repositories)) [:> Flex {:class "pt-2 text-center flex-col justify-center items-center" :gap "4"}
                                                       [:> Text {:size "1" :class "text-gray-8"}
                                                        "No repositories available."]]
        (and use-repositories? (not has-search?)) [repositories-view repositories filter-template-selected]
        (and use-repositories? has-search? (empty? filtered-data)) [:> Flex {:class "pt-2 text-center flex-col justify-center items-center" :gap "4"}
                                                                     [:> Text {:size "1" :class "text-gray-8"}
                                                                      (str "No runbooks matching \"" @search-term "\".")]]
        (empty? display-templates) [:> Flex {:class "pt-2 text-center flex-col justify-center items-center" :gap "4"}
                                    [:> Text {:size "1" :class "text-gray-8"}
                                     (if has-search?
                                       (str "No runbooks matching \"" @search-term "\".")
                                       "There are no runbooks available.")]]
        :else [directory-tree transformed-payload filter-template-selected nil]))))
